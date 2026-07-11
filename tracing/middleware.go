// Package tracing provides HTTP middleware for the miniargus agent: it
// wraps an http.Handler, captures method/path/status/latency/request ID,
// and fires the resulting span at the local agent over UDP -- fire-and-
// forget, so an unreachable or slow agent never adds latency to the
// wrapped handler.
package tracing

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"
)

const defaultAgentAddr = "127.0.0.1:8126"

type config struct {
	service   string
	agentAddr string
}

type Option func(*config)

func WithServiceName(name string) Option {
	return func(c *config) { c.service = name }
}

// WithAgentAddr overrides the local agent's trace UDP listener address
// (default 127.0.0.1:8126, matching the agent's default).
func WithAgentAddr(addr string) Option {
	return func(c *config) { c.agentAddr = addr }
}

type handler struct {
	next http.Handler
	cfg  config
	conn net.Conn // nil if the initial dial failed; sends are then no-ops
}

// Middleware wraps next, emitting one span per request to the local agent.
func Middleware(next http.Handler, opts ...Option) http.Handler {
	cfg := config{service: "unknown-service", agentAddr: defaultAgentAddr}
	for _, opt := range opts {
		opt(&cfg)
	}

	// UDP "Dial" doesn't perform a handshake -- it just resolves the address
	// and readies a socket for fire-and-forget Write calls, so this is cheap
	// even if the agent isn't up yet.
	conn, _ := net.Dial("udp", cfg.agentAddr)
	return &handler{next: next, cfg: cfg, conn: conn}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	traceID, parentID := traceContextFrom(r.Header.Get("traceparent"))
	requestID := r.Header.Get("X-Request-Id")
	if requestID == "" {
		requestID = newID(8)
	}

	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	h.next.ServeHTTP(rec, r)

	h.send(span{
		TS:         start,
		TraceID:    traceID,
		SpanID:     newID(8),
		ParentID:   parentID,
		Service:    h.cfg.service,
		Method:     r.Method,
		Path:       r.URL.Path,
		Status:     uint16(rec.status),
		DurationMS: float64(time.Since(start)) / float64(time.Millisecond),
		Tags:       map[string]string{"request_id": requestID},
	})
}

func (h *handler) send(s span) {
	if h.conn == nil {
		return
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return
	}
	_, _ = h.conn.Write(payload) // best-effort; errors aren't actionable here
}

// traceContextFrom parses a W3C traceparent header
// ("version-traceid-parentid-flags"); if absent or malformed, a fresh trace
// is started (this request becomes a root span).
func traceContextFrom(traceparent string) (traceID, parentID string) {
	parts := strings.Split(traceparent, "-")
	if len(parts) == 4 && len(parts[1]) == 32 && len(parts[2]) == 16 {
		return parts[1], parts[2]
	}
	return newID(16), ""
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}
