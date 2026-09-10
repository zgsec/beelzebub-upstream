package MCP

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	"github.com/beelzebub-labs/beelzebub/v3/internal/tracer"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPStructuredArgsRoundTripAndLegacyCommandIsUnchanged(t *testing.T) {
	// Regression: origin/main renders arguments as a Go map inside a pipe-delimited
	// string; a pipe in a value makes the result ambiguous and it is not JSON.
	arguments := map[string]any{"query": "left|right", "limit": float64(10)}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "logs.query", Arguments: arguments}}
	event := buildMCPEvent(
		context.Background(), "203.0.113.4:45100", "test", "result", request, time.Now(),
	)

	assert.Equal(t, fmt.Sprintf("%s|%s", request.Params.Name, request.Params.Arguments), event.Command)
	assert.Equal(t, "logs.query", event.Metadata["mcp.tool_name"])
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(event.Metadata["mcp.tool_args"]), &decoded))
	assert.Equal(t, arguments, decoded)
}

func TestMCPHandshakeMetadataIsBoundedAndJSONSafe(t *testing.T) {
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "tool", Arguments: map[string]any{}}}
	ctx := metadataClientContext(mcp.Implementation{
		Name:    strings.Repeat("a", mcpMetadataMaxBytes-1) + "é",
		Version: strings.Repeat("v", mcpMetadataMaxBytes+1),
	}, mcp.ClientCapabilities{Experimental: map[string]any{"large": strings.Repeat("x", mcpMetadataMaxBytes*4)}})
	event := buildMCPEvent(ctx, "203.0.113.4:1", "test", "ok", request, time.Now())

	for _, key := range []string{"mcp.client.name", "mcp.client.version", "mcp.client.capabilities"} {
		assert.LessOrEqual(t, len(event.Metadata[key]), mcpMetadataMaxBytes, key)
	}
	assert.Equal(t, strings.Repeat("a", mcpMetadataMaxBytes-1), event.Metadata["mcp.client.name"])
	assert.True(t, json.Valid([]byte(event.Metadata["mcp.client.capabilities"])))
	assert.Contains(t, event.Metadata["mcp.client.capabilities"], `"truncated":true`)
}

func TestMCPMalformedHandshakeEvidenceFailsOpen(t *testing.T) {
	bad := map[string]any{"cannot_marshal": make(chan int)}
	assert.Empty(t, boundedMCPJSON(bad))

	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "tool", Arguments: map[string]any{"ok": true}}}
	event := buildMCPEvent(metadataClientContext(mcp.Implementation{}, mcp.ClientCapabilities{Experimental: bad}), "203.0.113.4:1", "test", "served", request, time.Now())
	assert.Equal(t, "served", event.CommandOutput)
	assert.NotContains(t, event.Metadata, "mcp.client.capabilities")
}

func TestMCPClientEvidenceCannotChangeServedResult(t *testing.T) {
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "tool", Arguments: map[string]any{}}}
	anonymous := buildMCPEvent(context.Background(), "203.0.113.4:1", "test", "same-result", request, time.Now())
	claimedAdmin := buildMCPEvent(metadataClientContext(mcp.Implementation{Name: "admin-client"}, mcp.ClientCapabilities{}), "203.0.113.4:1", "test", "same-result", request, time.Now())
	assert.Equal(t, anonymous.CommandOutput, claimedAdmin.CommandOutput)
}

type metadataClient struct {
	server.ClientSession
	info mcp.Implementation
	caps mcp.ClientCapabilities
}

func (s *metadataClient) SessionID() string                                 { return "" }
func (s *metadataClient) GetClientInfo() mcp.Implementation                 { return s.info }
func (s *metadataClient) SetClientInfo(info mcp.Implementation)             { s.info = info }
func (s *metadataClient) GetClientCapabilities() mcp.ClientCapabilities     { return s.caps }
func (s *metadataClient) SetClientCapabilities(caps mcp.ClientCapabilities) { s.caps = caps }
func metadataClientContext(info mcp.Implementation, caps mcp.ClientCapabilities) context.Context {
	return server.NewMCPServer("test", "1").WithContext(context.Background(), &metadataClient{info: info, caps: caps})
}

type telemetryTrace chan tracer.Event

func (c telemetryTrace) TraceEvent(e tracer.Event) { c <- e }

func telemetryPost(t *testing.T, url, session, body string, modern bool) (string, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Close = true // Every POST uses a separate TCP connection.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	if modern {
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", "tools/call")
		req.Header.Set("Mcp-Name", "logs")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(data))
	var message map[string]any
	require.NoError(t, json.Unmarshal(data, &message))
	require.NotContains(t, message, "error", string(data))
	require.Contains(t, message, "result", string(data))
	return resp.Header.Get("Mcp-Session-Id"), message["result"].(map[string]any)
}

func telemetryEvent(t *testing.T, events telemetryTrace) tracer.Event {
	t.Helper()
	select {
	case e := <-events:
		for key := range e.Metadata {
			require.False(t, strings.HasPrefix(key, "session."), key)
		}
		require.NotContains(t, e.Metadata, "timing.since_prev_ms")
		require.NotContains(t, e.Metadata, "mcp.client.protocol_version")
		require.Contains(t, e.Metadata, "timing.latency_ms")
		elapsed, err := strconv.ParseInt(e.Metadata["timing.latency_ms"], 10, 64)
		require.NoError(t, err)
		require.GreaterOrEqual(t, elapsed, int64(0))
		require.Equal(t, "logs", e.Metadata["mcp.tool_name"])
		require.JSONEq(t, `{"filter":"x"}`, e.Metadata["mcp.tool_args"])
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("missing tool event")
		return tracer.Event{}
	}
}

func TestMCPMetadataFromLibraryContextOverHTTP(t *testing.T) {
	events := make(telemetryTrace, 32)
	s := &MCPStrategy{}
	t.Cleanup(func() { require.NoError(t, s.StopAll()) })
	conf := parser.BeelzebubServiceConfiguration{Address: "127.0.0.1:0", Protocol: "mcp",
		Tools: []parser.Tool{{Name: "logs", Handler: "ok", Params: []parser.Param{{Name: "filter"}}}}}
	require.NoError(t, s.Init(conf, events))
	url := "http://" + s.listeners[conf.Address].Addr().String() + "/mcp"
	initialize := func(version, name, clientVersion string) (string, string) {
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
			"protocolVersion": version, "capabilities": map[string]any{"experimental": map[string]any{"feature": true}},
			"clientInfo": map[string]any{"name": name, "version": clientVersion}}})
		require.NoError(t, err)
		sid, result := telemetryPost(t, url, "", string(body), false)
		require.NotEmpty(t, sid)
		return sid, result["protocolVersion"].(string)
	}
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"logs","arguments":{"filter":"x"}}}`
	sid, _ := initialize("2025-06-18", "legacy", "1")
	t.Run("initialized across connections", func(t *testing.T) {
		for range 2 {
			telemetryPost(t, url, sid, call, false)
			e := telemetryEvent(t, events)
			assert.Equal(t, sid, e.Metadata["mcp.session_id"])
			assert.Equal(t, "legacy", e.Metadata["mcp.client.name"])
			assert.Equal(t, "1", e.Metadata["mcp.client.version"])
			assert.JSONEq(t, `{"experimental":{"feature":true}}`, e.Metadata["mcp.client.capabilities"])
			assert.Equal(t, "2025-06-18", e.Metadata["mcp.protocol_version"])
		}
	})
	assertAnonymous := func(t *testing.T, e tracer.Event, id string) {
		t.Helper()
		assert.Equal(t, id, e.Metadata["mcp.session_id"])
		for key := range e.Metadata {
			assert.False(t, strings.HasPrefix(key, "mcp.client."), key)
		}
		assert.NotContains(t, e.Metadata, "mcp.protocol_version")
	}
	t.Run("reused uninitialized id", func(t *testing.T) {
		id := "mcp-session-00000000-0000-4000-8000-000000000001"
		for range 2 {
			telemetryPost(t, url, id, call, false)
			assertAnonymous(t, telemetryEvent(t, events), id)
		}
	})
	t.Run("second listener", func(t *testing.T) {
		other := conf
		other.Address = "127.0.0.2:0"
		require.NoError(t, s.Init(other, events))
		otherURL := "http://" + s.listeners[other.Address].Addr().String() + "/mcp"
		telemetryPost(t, otherURL, sid, call, false)
		assertAnonymous(t, telemetryEvent(t, events), sid)
	})
	t.Run("modern sessionless", func(t *testing.T) {
		for _, name := range []string{"modern-a", "modern-b"} {
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"logs","arguments":{"filter":"x"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":%q,"version":"2"},"io.modelcontextprotocol/clientCapabilities":{"experimental":{"modern":true}}}}}`, name)
			responseID, _ := telemetryPost(t, url, sid, body, true)
			assert.Empty(t, responseID)
			e := telemetryEvent(t, events)
			assert.NotContains(t, e.Metadata, "mcp.session_id")
			assert.Equal(t, name, e.Metadata["mcp.client.name"])
			assert.Equal(t, "2", e.Metadata["mcp.client.version"])
			assert.JSONEq(t, `{"experimental":{"modern":true}}`, e.Metadata["mcp.client.capabilities"])
			assert.Equal(t, "2026-07-28", e.Metadata["mcp.protocol_version"])
		}
	})
	t.Run("empty client fields and negotiated version", func(t *testing.T) {
		id, negotiated := initialize("2099-01-01", "", "")
		require.Equal(t, "2025-11-25", negotiated)
		telemetryPost(t, url, id, call, false)
		e := telemetryEvent(t, events)
		assert.NotContains(t, e.Metadata, "mcp.client.name")
		assert.NotContains(t, e.Metadata, "mcp.client.version")
		assert.Equal(t, negotiated, e.Metadata["mcp.protocol_version"])
	})
}

func TestMCPDeployTelemetryExceptionOverHTTP(t *testing.T) {
	events := make(telemetryTrace, 1)
	s := &MCPStrategy{}
	t.Cleanup(func() { require.NoError(t, s.StopAll()) })
	s.SetDeployFn(func(parser.BeelzebubServiceConfiguration) error { return nil })
	conf := parser.BeelzebubServiceConfiguration{Address: "127.0.0.1:0", Protocol: "mcp",
		Tools: []parser.Tool{{Name: deployDeployToolName, Params: []parser.Param{{Name: "config_yaml"}}}}}
	require.NoError(t, s.Init(conf, events))
	url := "http://" + s.listeners[conf.Address].Addr().String() + "/mcp"
	_, result := telemetryPost(t, url, "mcp-session-00000000-0000-4000-8000-000000000001", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"beelzebub:deploy","arguments":{"config_yaml":"protocol: ssh\naddress: ':2222'\n"}}}`, false)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "success")
	select {
	case e := <-events:
		assert.Equal(t, "Deployed honeypot via MCP", e.Msg)
		assert.Empty(t, e.Metadata)
	case <-time.After(5 * time.Second):
		t.Fatal("missing deploy event")
	}
}
