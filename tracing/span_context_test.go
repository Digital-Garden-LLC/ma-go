package tracing

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStartSpan_ChildInheritsTraceIDFromParent(t *testing.T) {
	root := &Span{traceID: "root-trace", spanID: "root-span", tags: map[string]string{}}
	ctx := context.WithValue(context.Background(), spanContextKey{}, root)

	_, child := StartSpan(ctx, "db.query")

	if child.traceID != "root-trace" {
		t.Errorf("child.traceID = %q, want %q", child.traceID, "root-trace")
	}
	if child.parentID != "root-span" {
		t.Errorf("child.parentID = %q, want %q", child.parentID, "root-span")
	}
	if child.spanID == "" || child.spanID == root.spanID {
		t.Errorf("child.spanID = %q, want a fresh non-empty id", child.spanID)
	}
}

func TestStartSpan_NoParentStartsNewRoot(t *testing.T) {
	_, s := StartSpan(context.Background(), "standalone-op")

	if s.traceID == "" {
		t.Error("expected a freshly generated trace ID")
	}
	if s.parentID != "" {
		t.Errorf("parentID = %q, want empty (this is a root)", s.parentID)
	}
}

func TestStartSpan_ChildOfChildNestsCorrectly(t *testing.T) {
	root := &Span{traceID: "t1", spanID: "root", tags: map[string]string{}}
	ctx := context.WithValue(context.Background(), spanContextKey{}, root)

	ctx, mid := StartSpan(ctx, "http.client")
	_, leaf := StartSpan(ctx, "db.query")

	if leaf.traceID != "t1" {
		t.Errorf("leaf.traceID = %q, want t1 (should still trace back to the original root)", leaf.traceID)
	}
	if leaf.parentID != mid.spanID {
		t.Errorf("leaf.parentID = %q, want mid span's id %q", leaf.parentID, mid.spanID)
	}
}

func TestSpan_FinishSendsOverUDP(t *testing.T) {
	addr, recv := startUDPCollector(t)
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	s := &Span{conn: conn, service: "worker", traceID: "t1", spanID: "s1", name: "db.query",
		start: time.Now().UTC(), tags: map[string]string{}}
	s.Finish()

	select {
	case got := <-recv:
		if got.Name != "db.query" {
			t.Errorf("Name = %q, want db.query", got.Name)
		}
		if got.Method != "" {
			t.Errorf("Method = %q, want empty for a non-HTTP span", got.Method)
		}
		if got.Service != "worker" || got.TraceID != "t1" || got.SpanID != "s1" {
			t.Errorf("got = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no span received")
	}
}

func TestSpan_FinishIsIdempotent(t *testing.T) {
	addr, recv := startUDPCollector(t)
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	s := &Span{conn: conn, traceID: "t1", spanID: "s1", name: "op", start: time.Now().UTC(), tags: map[string]string{}}
	s.Finish()
	s.Finish() // second call must not send a second packet
	s.Finish()

	<-recv // the one legitimate send
	select {
	case extra := <-recv:
		t.Fatalf("Finish sent more than once: got a second span %+v", extra)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing else arrives
	}
}

func TestSpan_SetErrorAddsErrorTags(t *testing.T) {
	addr, recv := startUDPCollector(t)
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	s := &Span{conn: conn, traceID: "t1", spanID: "s1", name: "op", start: time.Now().UTC(), tags: map[string]string{}}
	s.SetError(errors.New("boom"))
	s.Finish()

	got := <-recv
	if got.Tags["error"] != "true" {
		t.Errorf("tags[error] = %q, want true", got.Tags["error"])
	}
	if got.Tags["error.message"] != "boom" {
		t.Errorf("tags[error.message] = %q, want boom", got.Tags["error.message"])
	}
}

func TestSpan_SetErrorNilIsNoop(t *testing.T) {
	s := &Span{tags: map[string]string{}}
	s.SetError(nil)
	if len(s.tags) != 0 {
		t.Errorf("tags = %v, want untouched by a nil error", s.tags)
	}
}

func TestSpan_SetTagAfterFinishIsDroppedNotPanic(t *testing.T) {
	s := &Span{tags: map[string]string{}}
	s.Finish()                // conn is nil, so this just marks finished and returns
	s.SetTag("late", "value") // must not panic

	if _, ok := s.tags["late"]; ok {
		t.Error("tag set after Finish should have been dropped")
	}
}

func TestMiddleware_ChildSpanNestsUnderRootSpan(t *testing.T) {
	addr, recv := startUDPCollector(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, child := StartSpan(r.Context(), "db.query")
		child.SetTag("table", "orders")
		child.Finish()
		w.WriteHeader(http.StatusOK)
	})
	wrapped := Middleware(inner, WithServiceName("checkout-api"), WithAgentAddr(addr))

	req := httptest.NewRequest(http.MethodGet, "/checkout", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	var childSpan, rootSpan span
	for i := 0; i < 2; i++ {
		select {
		case s := <-recv:
			if s.Name == "db.query" {
				childSpan = s
			} else {
				rootSpan = s
			}
		case <-time.After(time.Second):
			t.Fatalf("only received %d of 2 expected spans", i)
		}
	}

	if childSpan.TraceID != rootSpan.TraceID {
		t.Errorf("child trace_id %q != root trace_id %q", childSpan.TraceID, rootSpan.TraceID)
	}
	if childSpan.ParentID != rootSpan.SpanID {
		t.Errorf("child parent_id %q != root span_id %q", childSpan.ParentID, rootSpan.SpanID)
	}
	if childSpan.Service != "checkout-api" {
		t.Errorf("child inherited service = %q, want checkout-api", childSpan.Service)
	}
	if childSpan.Tags["table"] != "orders" {
		t.Errorf("child tags = %v, missing table=orders", childSpan.Tags)
	}
	if rootSpan.Name != "" {
		t.Errorf("root span Name = %q, want empty (already fully identified by method/path)", rootSpan.Name)
	}
	if rootSpan.Method != http.MethodGet || rootSpan.Status != http.StatusOK {
		t.Errorf("root span = %+v, want GET/200", rootSpan)
	}
}
