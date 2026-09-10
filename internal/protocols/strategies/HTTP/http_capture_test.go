package HTTP

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	"github.com/beelzebub-labs/beelzebub/v3/internal/tracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureTracer struct{ events []tracer.Event }

func (c *captureTracer) TraceEvent(e tracer.Event) { c.events = append(c.events, e) }

func captureConf(t *testing.T) parser.BeelzebubServiceConfiguration {
	t.Helper()
	conf := parser.BeelzebubServiceConfiguration{
		Protocol:            "http",
		Address:             ":8080",
		Description:         "capture test",
		CaptureResponseBody: true,
		Commands: []parser.Command{{
			RegexStr:   "^/api$",
			Methods:    []string{"POST"},
			Handler:    `{"ok":true}`,
			StatusCode: 201,
		}},
	}
	require.NoError(t, conf.CompileCommandRegex())
	return conf
}

func TestCaptureDescriptors_CompleteBodyLeavesNoBodyDescriptors(t *testing.T) {
	tr := &captureTracer{}
	conf := captureConf(t)
	req := httptest.NewRequest("POST", "/api", strings.NewReader(`{"a":1}`))
	rec := httptest.NewRecorder()
	newHTTPHandler(conf, tr)(rec, req)

	if assert.Len(t, tr.events, 1) {
		m := tr.events[0].Metadata
		assert.Equal(t, "201", m["http.response.status"])
		assert.NotContains(t, m, "body.request.truncated", "a complete body is described by Body alone")
		assert.NotContains(t, m, "body.request.read_budget")
		assert.Equal(t, `{"a":1}`, tr.events[0].Body)
		assert.Equal(t, `{"ok":true}`, tr.events[0].CommandOutput)
	}
	assert.Equal(t, 201, rec.Code)
}

func TestCaptureDescriptors_OversizeBodyIsMarkedTruncated(t *testing.T) {
	// Regression: origin/main retains a bounded Body but records nothing that
	// tells a consumer the body was cut.
	tr := &captureTracer{}
	conf := captureConf(t)
	big := strings.Repeat("x", maxRequestBodyBytes+512)
	req := httptest.NewRequest("POST", "/api", strings.NewReader(big))
	newHTTPHandler(conf, tr)(httptest.NewRecorder(), req)

	if assert.Len(t, tr.events, 1) {
		m := tr.events[0].Metadata
		assert.Equal(t, "true", m["body.request.truncated"])
		assert.Equal(t, "1048576", m["body.request.read_budget"])
		assert.Len(t, tr.events[0].Body, maxRequestBodyBytes)
	}
}

func TestCaptureDescriptors_ExactlyAtBudgetIsComplete(t *testing.T) {
	tr := &captureTracer{}
	conf := captureConf(t)
	body := strings.Repeat("x", maxRequestBodyBytes)
	req := httptest.NewRequest("POST", "/api", strings.NewReader(body))
	newHTTPHandler(conf, tr)(httptest.NewRecorder(), req)

	if assert.Len(t, tr.events, 1) {
		assert.NotContains(t, tr.events[0].Metadata, "body.request.truncated")
		assert.Equal(t, body, tr.events[0].Body, "look-ahead must not alter retained bytes")
	}
}

func TestCaptureDescriptors_ResponseContentIsOptIn(t *testing.T) {
	tr := &captureTracer{}
	conf := captureConf(t)
	conf.CaptureResponseBody = false
	req := httptest.NewRequest("POST", "/api", strings.NewReader(`{"a":1}`))
	newHTTPHandler(conf, tr)(httptest.NewRecorder(), req)

	if assert.Len(t, tr.events, 1) {
		assert.Empty(t, tr.events[0].CommandOutput)
		assert.Equal(t, "201", tr.events[0].Metadata["http.response.status"])
	}
}

func TestCaptureDescriptors_MethodNotAllowedPathIsDescribed(t *testing.T) {
	tr := &captureTracer{}
	conf := captureConf(t)
	req := httptest.NewRequest("GET", "/api", nil)
	rec := httptest.NewRecorder()
	newHTTPHandler(conf, tr)(rec, req)

	assert.Equal(t, 405, rec.Code)
	if assert.Len(t, tr.events, 1) {
		m := tr.events[0].Metadata
		assert.Equal(t, "405", m["http.response.status"])
		assert.NotContains(t, m, "body.request.truncated", "no body: no body descriptors")
		assert.NotContains(t, m, "body.request.read_budget")
		assert.Equal(t, "Method Not Allowed", tr.events[0].CommandOutput, "opt-in retains the 405 body too")
	}
}

func TestCaptureDescriptors_MethodNotAllowedHonorsResponseOptIn(t *testing.T) {
	tr := &captureTracer{}
	conf := captureConf(t)
	conf.CaptureResponseBody = false
	rec := httptest.NewRecorder()
	newHTTPHandler(conf, tr)(rec, httptest.NewRequest("GET", "/api", nil))

	assert.Equal(t, 405, rec.Code)
	if assert.Len(t, tr.events, 1) {
		assert.Empty(t, tr.events[0].CommandOutput, "captureResponseBody=false must not retain the 405 body")
		assert.Equal(t, "405", tr.events[0].Metadata["http.response.status"])
	}
}

type partialErrorReader struct {
	read bool
}

func (r *partialErrorReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	return copy(p, "partial"), errors.New("read failed")
}

func TestCaptureDescriptors_ReadErrorRetainsAndMarksPartialBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/api", nil)
	req.Body = io.NopCloser(&partialErrorReader{})

	body, metadata := readRequestBody(req)

	assert.Equal(t, "partial", body)
	assert.Equal(t, "read failed", metadata["body.request.read_error"])
	assert.NotContains(t, metadata, "body.request.truncated", "a read error is not budget truncation")
	assert.NotContains(t, metadata, "body.request.read_budget")
}

func TestCaptureDescriptors_StatusMatchesImplicitWireStatus(t *testing.T) {
	conf := captureConf(t)
	conf.Commands[0].Methods = []string{"GET"}
	conf.Commands[0].StatusCode = 444
	tr := &captureTracer{}
	recorder := httptest.NewRecorder()

	newHTTPHandler(conf, tr)(recorder, httptest.NewRequest("GET", "/api", nil))

	assert.Equal(t, 200, recorder.Code)
	if assert.Len(t, tr.events, 1) {
		assert.Equal(t, "200", tr.events[0].Metadata["http.response.status"])
	}
}
