package MCP

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	"github.com/beelzebub-labs/beelzebub/v3/internal/tracer"
	"github.com/google/uuid"
)

// Capture is a bounded application-layer observation, not a packet capture.
// In particular, an early rejection may leave the request body unread.
const captureLimit = 64 * 1024

type captureConnKey struct{}
type captureConnection struct {
	id  string
	seq atomic.Uint64
}

func captureConnContext(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, captureConnKey{}, &captureConnection{id: uuid.NewString()})
}

type capturedPrefix struct {
	data     []byte
	observed int64
}

func (p *capturedPrefix) add(b []byte) {
	p.observed += int64(len(b))
	if left := captureLimit - len(p.data); left > 0 {
		p.data = append(p.data, b[:min(left, len(b))]...)
	}
}

func (p *capturedPrefix) encode(meta map[string]string, key string, complete bool) string {
	meta[key+".observed_bytes"] = strconv.FormatInt(p.observed, 10)
	meta[key+".truncated"] = strconv.FormatBool(p.observed > int64(len(p.data)))
	meta[key+".complete"] = strconv.FormatBool(complete && p.observed == int64(len(p.data)))
	if !utf8.Valid(p.data) {
		meta[key+".encoding"] = "base64"
		return base64.StdEncoding.EncodeToString(p.data)
	}
	return string(p.data)
}

type captureReader struct {
	io.ReadCloser
	prefix capturedPrefix
	eof    bool
	err    error
}

func (r *captureReader) Read(b []byte) (int, error) {
	n, err := r.ReadCloser.Read(b)
	r.prefix.add(b[:n])
	if err == io.EOF {
		r.eof = true
	} else if err != nil {
		r.err = err
	}
	return n, err
}

type captureWriter struct {
	http.ResponseWriter
	prefix capturedPrefix
	status int
	err    error
}

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *captureWriter) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.prefix.add(b[:n])
	if err != nil {
		w.err = err
	}
	return n, err
}

// MCP can stream SSE responses. Forward flushes without buffering the stream.
func (w *captureWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		w.err = err
	}
}

func captureRequests(next http.Handler, conf parser.BeelzebubServiceConfiguration, tr tracer.Tracer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET is a potentially indefinite SSE subscription, not a request/reply.
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		meta := map[string]string{"capture.kind": "mcp.request",
			"capture.started_at":     started.UTC().Format(time.RFC3339Nano),
			"request.content_length": strconv.FormatInt(r.ContentLength, 10),
			"request.content_type":   r.Header.Get("Content-Type"),
		}
		if version := r.Header.Get("MCP-Protocol-Version"); version != "" {
			meta["mcp.protocol_version"] = version
		}
		if conn, ok := r.Context().Value(captureConnKey{}).(*captureConnection); ok {
			meta["connection.id"] = conn.id
			meta["connection.request_seq"] = strconv.FormatUint(conn.seq.Add(1), 10)
		}
		reader := &captureReader{ReadCloser: r.Body}
		r.Body = reader
		writer := &captureWriter{ResponseWriter: w}
		completed := false
		defer func() {
			meta["capture.duration_us"] = strconv.FormatInt(time.Since(started).Microseconds(), 10)
			meta["capture.handler_completed"] = strconv.FormatBool(completed)
			if writer.status == 0 && completed {
				writer.status = http.StatusOK
			}
			meta["response.status"] = strconv.Itoa(writer.status)
			meta["response.content_type"] = w.Header().Get("Content-Type")
			// The server assigns the session on initialize; afterwards the client presents it.
			if session := w.Header().Get("Mcp-Session-Id"); session != "" {
				meta["mcp.session_id"] = session
			} else if session := r.Header.Get("Mcp-Session-Id"); session != "" {
				meta["mcp.session_id"] = session
			}
			if reader.err != nil {
				meta["request.read_error"] = reader.err.Error()
			}
			if writer.err != nil {
				meta["response.write_error"] = writer.err.Error()
			}
			requestComplete := reader.err == nil && (reader.eof || (r.ContentLength >= 0 && reader.prefix.observed == r.ContentLength))
			body := reader.prefix.encode(meta, "request", requestComplete)
			command := ""
			if meta["request.complete"] == "true" && utf8.Valid(reader.prefix.data) {
				// Match the JSON member exactly; struct decoding also accepts "Method".
				var message map[string]json.RawMessage
				if json.Unmarshal(reader.prefix.data, &message) == nil {
					var method string
					if json.Unmarshal(message["method"], &method) == nil {
						command = method
					}
				}
			}
			reply := writer.prefix.encode(meta, "response", completed && writer.err == nil)
			host, port, _ := net.SplitHostPort(r.RemoteAddr)
			tr.TraceEvent(tracer.Event{ID: uuid.NewString(), Msg: "MCP request observation",
				Protocol: tracer.MCP.String(), Status: tracer.Stateless.String(),
				RemoteAddr: r.RemoteAddr, SourceIp: host, SourcePort: port,
				Description: conf.Description, Command: command, Body: body,
				CommandOutput: reply, Metadata: meta})
		}()
		next.ServeHTTP(writer, r)
		completed = true
	})
}
