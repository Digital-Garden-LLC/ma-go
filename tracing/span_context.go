package tracing

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"time"
)

// spanContextKey is the context.Context key the current span is stored
// under -- unexported, so the only way to read or install it is through
// this package's own API.
type spanContextKey struct{}

// Span represents one node in a request's trace tree. Middleware creates
// the root span for each incoming HTTP request and installs it on the
// request's context; call StartSpan from inside a handler (or anything it
// calls) to record a nested operation -- a DB query, a downstream HTTP
// call, a cache lookup -- as a child of whatever span is current on ctx.
//
// Fields are only ever mutated through SetTag/SetError/Finish, all of
// which take mu, so a Span is safe to hand to a goroutine.
type Span struct {
	// Immutable after construction -- read without a lock.
	conn     net.Conn // shared with the root span; nil sends are no-ops
	service  string
	traceID  string
	spanID   string
	parentID string
	name     string
	start    time.Time
	isHTTP   bool // true only for Middleware's root span, see Finish

	mu       sync.Mutex
	tags     map[string]string
	finished bool

	// Set once by Middleware, after the wrapped handler returns and before
	// Finish -- single-writer (ServeHTTP), so no lock needed for these.
	method string
	path   string
	status uint16
}

// StartSpan starts a new child span named name, nested under whatever span
// is current in ctx (typically the request's root span, installed by
// Middleware). If ctx carries no span -- e.g. called outside a traced
// request, or in a test -- it starts a new root span of its own instead of
// panicking or silently doing nothing, so it's always safe to call.
//
// Returns a context carrying the new span as current -- pass this to
// anything the operation itself calls, so further nesting works -- and the
// span itself. Call Finish (typically deferred immediately) when the
// operation completes:
//
//	ctx, span := tracing.StartSpan(ctx, "db.query")
//	defer span.Finish()
//	rows, err := db.QueryContext(ctx, "...")
//	if err != nil {
//	    span.SetError(err)
//	}
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	parent, _ := ctx.Value(spanContextKey{}).(*Span)

	s := &Span{
		name:   name,
		start:  time.Now().UTC(),
		spanID: newID(8),
		tags:   map[string]string{},
	}
	if parent != nil {
		s.conn = parent.conn
		s.service = parent.service
		s.traceID = parent.traceID
		s.parentID = parent.spanID
	} else {
		s.traceID = newID(16)
	}
	return context.WithValue(ctx, spanContextKey{}, s), s
}

// SetTag attaches a key/value tag to the span, sent to miniargus as part of
// its tags. Call before Finish -- tags set afterward are silently dropped,
// since the span has already been sent.
func (s *Span) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	s.tags[key] = value
}

// SetError marks the span as failed and records err's message as a tag --
// a convenience for the single most common thing a span needs to report
// beyond its duration. A no-op if err is nil, so it's safe to call
// unconditionally: span.SetError(err) after any operation that returns one.
func (s *Span) SetError(err error) {
	if err == nil {
		return
	}
	s.SetTag("error", "true")
	s.SetTag("error.message", err.Error())
}

// Finish computes the span's duration and sends it to the agent.
// Idempotent -- only the first call actually sends, so it's safe to defer
// unconditionally even if an error path also calls it explicitly.
func (s *Span) Finish() {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true

	wire := span{
		TS:         s.start,
		TraceID:    s.traceID,
		SpanID:     s.spanID,
		ParentID:   s.parentID,
		Service:    s.service,
		DurationMS: float64(time.Since(s.start)) / float64(time.Millisecond),
		Tags:       s.tags,
	}
	s.mu.Unlock()

	// Method/Path/Status are HTTP-specific wire fields with no real
	// equivalent for a generic operation span. Rather than adding a new
	// wire field today (which would need a matching change on the
	// ingestion/ClickHouse side to actually be queryable), a child span's
	// name is carried in Method -- e.g. "db.query" sits in the same slot
	// "GET" would for the root HTTP span. This is a pragmatic reuse of the
	// existing wire format, not a clean model: Path/Status stay empty/zero
	// for a non-HTTP span, and "db.query" next to "GET" in the same column
	// reads oddly. A dedicated name/kind field, queryable independently of
	// the HTTP-shaped columns, is the real fix -- that's a miniargus-side
	// ingestion/schema change, out of scope for this SDK-only change.
	if s.isHTTP {
		wire.Method = s.method
		wire.Path = s.path
		wire.Status = s.status
	} else {
		wire.Method = s.name
	}

	if s.conn == nil {
		return
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return
	}
	_, _ = s.conn.Write(payload) // best-effort; errors aren't actionable here
}
