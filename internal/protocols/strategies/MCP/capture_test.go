package MCP

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	"github.com/beelzebub-labs/beelzebub/v3/internal/tracer"
	"github.com/stretchr/testify/require"
)

type captureTrace chan tracer.Event

func (c captureTrace) TraceEvent(e tracer.Event) { c <- e }

func observation(t *testing.T, events captureTrace) tracer.Event {
	t.Helper()
	for {
		select {
		case e := <-events:
			if e.Metadata["capture.kind"] == "mcp.request" {
				return e
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing request observation")
			return tracer.Event{}
		}
	}
}

func TestCaptureRealMCP(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			events := make(captureTrace, 64)
			conf := parser.BeelzebubServiceConfiguration{Address: "127.0.0.1:0", Protocol: "mcp", Description: "capture-test", CaptureMCPRequests: enabled,
				Tools: []parser.Tool{{Name: "logs", Description: "logs", Handler: "fixed reply", Params: []parser.Param{{Name: "filter", Description: "filter"}}}}}
			s := &MCPStrategy{}
			require.NoError(t, s.Init(conf, events))
			t.Cleanup(func() { require.NoError(t, s.StopAll()) })
			transport := &http.Transport{MaxConnsPerHost: 1}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			url := "http://" + s.listeners[conf.Address].Addr().String() + "/mcp"
			session, connection := "", ""
			inputs := []string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"synthetic-test","version":"1"}}}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"/app/.env"}}}`,
				`{"jsonrpc":"2.0","id":3,"method":"unknown/method","params":{"path":"/etc/shadow"}}`,
				`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"logs","arguments":{"filter":"auth|failed [x:y]"}}}`,
				`{"jsonrpc":`,
			}
			methods := []string{"initialize", "tools/list", "tools/call", "unknown/method", "tools/call", ""}
			for i, body := range inputs {
				req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				if session != "" {
					req.Header.Set("Mcp-Session-Id", session)
					req.Header.Set("MCP-Protocol-Version", "2025-06-18")
				}
				resp, err := client.Do(req)
				require.NoError(t, err)
				reply, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				if i == 0 {
					session = resp.Header.Get("Mcp-Session-Id")
					require.NotEmpty(t, session)
					require.Contains(t, string(reply), "serverInfo")
				}
				if i == 4 {
					require.Contains(t, string(reply), "fixed reply")
				}
				if enabled {
					e := observation(t, events)
					require.Equal(t, body, e.Body)
					require.Equal(t, methods[i], e.Command)
					if i == 0 {
						require.NotContains(t, e.Metadata, "mcp.protocol_version")
					} else {
						require.Equal(t, "2025-06-18", e.Metadata["mcp.protocol_version"])
					}
					require.Equal(t, string(reply), e.CommandOutput)
					require.Equal(t, fmt.Sprint(resp.StatusCode), e.Metadata["response.status"])
					require.Equal(t, "true", e.Metadata["request.complete"])
					require.Equal(t, "true", e.Metadata["response.complete"])
					require.Equal(t, fmt.Sprint(i+1), e.Metadata["connection.request_seq"])
					if i == 0 {
						connection = e.Metadata["connection.id"]
						require.NotEmpty(t, connection)
					}
					require.Equal(t, connection, e.Metadata["connection.id"])
					require.Equal(t, session, e.Metadata["mcp.session_id"])
					require.NotContains(t, e.Metadata, "request.encoding")
					require.NotContains(t, e.Metadata, "response.encoding")
				}
			}
			if !enabled {
				select {
				case e := <-events:
					require.Nil(t, e.Metadata)
					require.Contains(t, e.Command, "logs|")
				case <-time.After(time.Second):
					t.Fatal("legacy event lost")
				}
				require.Empty(t, events)
			}
		})
	}
}

func TestCaptureBoundsAndEarlyRejection(t *testing.T) {
	for _, size := range []int{0, captureLimit, captureLimit + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			body := ""
			if size > 0 {
				body = `{"method":"tools/call"}`
				body += strings.Repeat(" ", size-len(body))
			}
			events := make(captureTrace, 1)
			h := captureRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				w.WriteHeader(http.StatusAccepted)
				_, err = w.Write(b)
				require.NoError(t, err)
				w.(http.Flusher).Flush()
			}), parser.BeelzebubServiceConfiguration{}, events)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
			e := observation(t, events)
			require.Equal(t, body, w.Body.String())
			require.Len(t, e.Body, min(size, captureLimit))
			require.Len(t, e.CommandOutput, min(size, captureLimit))
			if size == captureLimit {
				require.Equal(t, "tools/call", e.Command)
			} else {
				require.Empty(t, e.Command)
			}
			if size > captureLimit {
				require.True(t, json.Valid([]byte(e.Body)), "even a valid JSON prefix must not supply a method")
			}
			for _, direction := range []string{"request", "response"} {
				require.Equal(t, fmt.Sprint(size <= captureLimit), e.Metadata[direction+".complete"])
				require.Equal(t, fmt.Sprint(size > captureLimit), e.Metadata[direction+".truncated"])
				require.Equal(t, fmt.Sprint(size), e.Metadata[direction+".observed_bytes"])
			}
		})
	}
	for _, length := range []int64{-1, 7} {
		events := make(captureTrace, 1)
		h := captureRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(415) }), parser.BeelzebubServiceConfiguration{}, events)
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader("notread"))
		r.ContentLength = length
		h.ServeHTTP(httptest.NewRecorder(), r)
		e := observation(t, events)
		require.Empty(t, e.Body)
		require.Empty(t, e.Command)
		require.Equal(t, "false", e.Metadata["request.complete"])
		require.Equal(t, "false", e.Metadata["request.truncated"])
	}
}

type brokenCaptureReader struct{}

func (brokenCaptureReader) Read(b []byte) (int, error) {
	return copy(b, "partial"), errors.New("read failed")
}
func (brokenCaptureReader) Close() error { return nil }

type brokenCaptureWriter struct{ *httptest.ResponseRecorder }

func (w brokenCaptureWriter) Write(b []byte) (int, error) {
	n, _ := w.ResponseRecorder.Write(b[:2])
	return n, errors.New("write failed")
}

func TestCaptureErrorsAndBinary(t *testing.T) {
	events := make(captureTrace, 1)
	h := captureRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("reply"))
	}), parser.BeelzebubServiceConfiguration{}, events)
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Body = brokenCaptureReader{}
	r.ContentLength = -1
	h.ServeHTTP(brokenCaptureWriter{httptest.NewRecorder()}, r)
	e := observation(t, events)
	require.Equal(t, "partial", e.Body)
	require.Empty(t, e.Command)
	require.Equal(t, "re", e.CommandOutput)
	require.Equal(t, "false", e.Metadata["request.complete"])
	require.Equal(t, "false", e.Metadata["response.complete"])
	require.Equal(t, "read failed", e.Metadata["request.read_error"])
	require.Equal(t, "write failed", e.Metadata["response.write_error"])
	binary := []byte{0xff, 0xfe, 0}
	r = httptest.NewRequest("POST", "/mcp", bytes.NewReader(binary))
	h.ServeHTTP(httptest.NewRecorder(), r)
	e = observation(t, events)
	require.Equal(t, "base64", e.Metadata["request.encoding"])
	require.Empty(t, e.Command)
	decoded, err := base64.StdEncoding.DecodeString(e.Body)
	require.NoError(t, err)
	require.Equal(t, binary, decoded)
}

func TestCaptureMethodAndProtocolHeader(t *testing.T) {
	for _, tc := range []struct {
		name, body, method, version string
	}{
		{"notification", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "notifications/initialized", "2025-06-18"},
		{"response", `{"jsonrpc":"2.0","id":1,"result":{}}`, "", ""},
		{"no method", `{}`, "", ""},
		{"null method", `{"method":null}`, "", ""},
		{"numeric method", `{"method":42}`, "", ""},
		{"object method", `{"method":{}}`, "", ""},
		{"empty method", `{"method":""}`, "", ""},
		{"wrong case", `{"Method":"tools/list"}`, "", ""},
		{"array", `[{"method":"tools/list"}]`, "", ""},
		{"null", `null`, "", ""},
		{"trailing JSON", `{"method":"tools/list"} {}`, "", ""},
		{"invalid UTF-8", "{\"method\":\"tools/list\",\"extra\":\"\xff\"}", "", ""},
		{"unvalidated header", `{"method":"tools/list"}`, "tools/list", "not-a-version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make(captureTrace, 1)
			h := captureRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := io.Copy(io.Discard, r.Body)
				require.NoError(t, err)
				w.WriteHeader(http.StatusAccepted)
			}), parser.BeelzebubServiceConfiguration{}, events)
			r := httptest.NewRequest("POST", "/mcp", strings.NewReader(tc.body))
			r.Header.Set("MCP-Protocol-Version", tc.version)
			h.ServeHTTP(httptest.NewRecorder(), r)
			e := observation(t, events)
			require.Equal(t, tc.method, e.Command)
			require.Equal(t, "true", e.Metadata["request.complete"])
			if tc.version == "" {
				require.NotContains(t, e.Metadata, "mcp.protocol_version")
			} else {
				require.Equal(t, tc.version, e.Metadata["mcp.protocol_version"])
			}
		})
	}
}

func TestCaptureDoesNotObserveGET(t *testing.T) {
	events := make(captureTrace, 1)
	h := captureRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("unchanged")) }), parser.BeelzebubServiceConfiguration{}, events)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/mcp", nil))
	require.Equal(t, "unchanged", w.Body.String())
	require.Empty(t, events)
}

func TestCaptureRecordsHandlerPanic(t *testing.T) {
	events := make(captureTrace, 1)
	h := captureRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		panic("handler failed")
	}), parser.BeelzebubServiceConfiguration{}, events)
	require.PanicsWithValue(t, "handler failed", func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0"}`)))
	})
	e := observation(t, events)
	require.Equal(t, `{"jsonrpc":"2.0"}`, e.Body)
	require.Equal(t, "false", e.Metadata["capture.handler_completed"])
	require.Equal(t, "true", e.Metadata["request.complete"])
	require.Equal(t, "false", e.Metadata["response.complete"])
	require.Equal(t, "0", e.Metadata["response.status"])
}
