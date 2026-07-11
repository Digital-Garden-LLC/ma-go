// Package tracing provides HTTP middleware for the miniargus agent: it
// wraps an http.Handler, captures method/path/status/latency/request ID,
// and fires the resulting span at the local agent over UDP -- fire-and-
// forget, so an unreachable or slow agent never adds latency to the
// wrapped handler.
package tracing

import (
	"context"
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
	traceID, parentID := traceContextFrom(r.Header.Get("traceparent"))
	requestID := r.Header.Get("X-Request-Id")
	if requestID == "" {
		requestID = newID(8)
	}

	// The root span is installed on the request's context so handler code
	// (and anything it calls) can nest child spans under it via StartSpan
	// -- see span_context.go. isHTTP marks it for Finish's Method/Path/
	// Status handling, which only ever applies to this one span per
	// request.
	root := &Span{
		conn:     h.conn,
		service:  h.cfg.service,
		traceID:  traceID,
		spanID:   newID(8),
		parentID: parentID,
		start:    time.Now().UTC(),
		tags:     map[string]string{"request_id": requestID},
		isHTTP:   true,
	}
	ctx := context.WithValue(r.Context(), spanContextKey{}, root)

	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	h.next.ServeHTTP(rec, r.WithContext(ctx))

	root.method = r.Method
	root.path = r.URL.Path
	root.status = uint16(rec.status)
	root.Finish()
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
