package HTTP

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	pluginapi "github.com/beelzebub-labs/beelzebub/v3/pkg/plugin"
	"github.com/stretchr/testify/require"
)

type bodyRecordingHTTPPlugin struct {
	name string
	body []byte
	err  error
}

func (p *bodyRecordingHTTPPlugin) Metadata() pluginapi.Metadata {
	return pluginapi.Metadata{Name: p.name}
}

func (p *bodyRecordingHTTPPlugin) HandleHTTP(request *http.Request) pluginapi.HTTPResponse {
	p.body, p.err = io.ReadAll(request.Body)
	return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: "ok"}
}

func TestHTTPPluginReceivesCapturedRequestBody(t *testing.T) {
	selected := &bodyRecordingHTTPPlugin{name: "test-http-plugin-request-body"}
	pluginapi.Register(selected)
	requestBody := `{"model":"example","messages":[]}`
	request := httptest.NewRequest(
		http.MethodPost,
		"http://localhost/v1/chat/completions",
		strings.NewReader(requestBody),
	)
	request.RemoteAddr = "192.0.2.10:12345"

	_, err := buildHTTPResponse(
		parser.BeelzebubServiceConfiguration{},
		&mockTracer{},
		parser.Command{Plugin: selected.name, StatusCode: http.StatusOK},
		request,
	)

	require.NoError(t, err)
	require.NoError(t, selected.err)
	require.Equal(t, []byte(requestBody), selected.body)
}

func TestHTTPPluginReceivesBodyBeyondTelemetryCaptureLimit(t *testing.T) {
	selected := &bodyRecordingHTTPPlugin{name: "test-http-plugin-large-request-body"}
	pluginapi.Register(selected)
	requestBody := strings.Repeat("x", maxHTTPBodyCaptureBytes+17)
	request := httptest.NewRequest(http.MethodPost, "http://localhost/upload", strings.NewReader(requestBody))
	request.RemoteAddr = "192.0.2.10:12345"
	tracer := &mockTracer{}

	_, err := buildHTTPResponse(
		parser.BeelzebubServiceConfiguration{},
		tracer,
		parser.Command{Plugin: selected.name, StatusCode: http.StatusOK},
		request,
	)

	require.NoError(t, err)
	require.NoError(t, selected.err)
	require.Equal(t, []byte(requestBody), selected.body)
	require.Len(t, tracer.events, 1)
	require.Len(t, tracer.events[0].Body, maxHTTPBodyCaptureBytes)
}
