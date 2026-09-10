package HTTP

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	"github.com/beelzebub-labs/beelzebub/v3/internal/tracer"
	"github.com/beelzebub-labs/beelzebub/v3/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sessionTestConfig(t *testing.T) parser.BeelzebubServiceConfiguration {
	t.Helper()
	conf := parser.BeelzebubServiceConfiguration{Commands: []parser.Command{{
		Name: "root", RegexStr: "^/$", Handler: "ok", StatusCode: 200,
	}}}
	require.NoError(t, conf.CompileCommandRegex())
	return conf
}

func sessionRequest(remote string, session *tracer.EventSession) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = remote
	return req.WithContext(context.WithValue(req.Context(), httpSessionCtxKey{}, session))
}

func TestHTTPSessionSameNATDoesNotMergeClients(t *testing.T) {
	tr := &mockTracer{}
	handler := newHTTPHandler(sessionTestConfig(t), tr)
	handler(httptest.NewRecorder(), sessionRequest("203.0.113.8:41001", tracer.NewEventSession("client-a")))
	handler(httptest.NewRecorder(), sessionRequest("203.0.113.8:41002", tracer.NewEventSession("client-b")))

	require.Len(t, tr.events, 2)
	assert.Equal(t, tr.events[0].SourceIp, tr.events[1].SourceIp)
	assert.NotEqual(t, tr.events[0].Metadata["session.key"], tr.events[1].Metadata["session.key"])
}

func TestHTTPSessionReusesKeyAndIncrementsSequence(t *testing.T) {
	// Regression: origin/main mints an unrelated event UUID per request and has
	// no reusable session.key or monotonic session.seq.
	tr := &mockTracer{}
	handler := newHTTPHandler(sessionTestConfig(t), tr)
	session := tracer.NewEventSession("one-connection")
	for i := 0; i < 3; i++ {
		handler(httptest.NewRecorder(), sessionRequest("203.0.113.8:41001", session))
	}

	require.Len(t, tr.events, 3)
	for i, event := range tr.events {
		assert.Equal(t, "one-connection", event.Metadata["session.key"])
		assert.Equal(t, strconv.Itoa(i), event.Metadata["session.seq"])
		assert.NotEmpty(t, event.Metadata["timing.latency_ms"])
		assert.NotEmpty(t, event.Metadata["timing.since_prev_ms"])
	}
}

func TestHTTPSessionDoesNotClaimContinuityAcrossConnections(t *testing.T) {
	tr := &mockTracer{}
	handler := newHTTPHandler(sessionTestConfig(t), tr)
	handler(httptest.NewRecorder(), sessionRequest("203.0.113.8:41001", tracer.NewEventSession("connection-a")))
	handler(httptest.NewRecorder(), sessionRequest("203.0.113.8:41002", tracer.NewEventSession("connection-b")))

	require.Len(t, tr.events, 2)
	assert.NotEqual(t, tr.events[0].Metadata["session.key"], tr.events[1].Metadata["session.key"])
}

type slowCommandPlugin struct{ delay time.Duration }

func (p *slowCommandPlugin) Metadata() plugin.Metadata {
	return plugin.Metadata{Name: "http-session-slow-cmd"}
}

func (p *slowCommandPlugin) Execute(_ context.Context, _ plugin.CommandRequest) (string, error) {
	time.Sleep(p.delay)
	return "slow-output", nil
}

func TestHTTPTimingLatencyCoversTheHandler(t *testing.T) {
	// The event is emitted once the response is decided, so timing.latency_ms
	// includes plugin execution rather than only request matching.
	plugin.Register(&slowCommandPlugin{delay: 50 * time.Millisecond})
	conf := parser.BeelzebubServiceConfiguration{Commands: []parser.Command{{
		Name: "slow", RegexStr: "^/slow$", Plugin: "http-session-slow-cmd",
	}}}
	require.NoError(t, conf.CompileCommandRegex())
	tr := &mockTracer{}

	req := httptest.NewRequest("GET", "/slow", nil)
	newHTTPHandler(conf, tr)(httptest.NewRecorder(), req)

	require.Len(t, tr.events, 1)
	latency, err := strconv.Atoi(tr.events[0].Metadata["timing.latency_ms"])
	require.NoError(t, err)
	assert.GreaterOrEqual(t, latency, 50)
	assert.Equal(t, "200", tr.events[0].Metadata["http.response.status"])
}
