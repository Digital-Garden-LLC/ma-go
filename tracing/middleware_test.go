package tracing

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTraceContextFrom_ValidTraceparent(t *testing.T) {
	// version-traceid(32 hex)-parentid(16 hex)-flags
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	traceID, parentID := traceContextFrom(tp)
	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("traceID = %q", traceID)
	}
	if parentID != "00f067aa0ba902b7" {
		t.Errorf("parentID = %q", parentID)
	}
}

func TestTraceContextFrom_MissingOrMalformed(t *testing.T) {
	for _, tp := range []string{"", "not-a-traceparent", "00-tooshort-00f067aa0ba902b7-01"} {
		traceID, parentID := traceContextFrom(tp)
		if traceID == "" {
			t.Errorf("traceContextFrom(%q): expected a generated trace ID, got empty", tp)
		}
		if parentID != "" {
			t.Errorf("traceContextFrom(%q): expected no parent for a root span, got %q", tp, parentID)
		}
	}
}

func TestNewID_LengthAndUniqueness(t *testing.T) {
	a := newID(16)
	b := newID(16)
	if len(a) != 32 { // 16 bytes -> 32 hex chars
		t.Errorf("len(newID(16)) = %d, want 32", len(a))
	}
	if a == b {
		t.Error("two calls to newID produced the same value")
	}
}

func startUDPCollector(t *testing.T) (addr string, recv <-chan span) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	ch := make(chan span, 10)
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			var s span
			if json.Unmarshal(buf[:n], &s) == nil {
				ch <- s
			}
		}
	}()
	return conn.LocalAddr().String(), ch
}

func TestMiddleware_SendsSpanOverUDP(t *testing.T) {
	addr, recv := startUDPCollector(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	})
	wrapped := Middleware(inner, WithServiceName("checkout-api"), WithAgentAddr(addr))

	req := httptest.NewRequest(http.MethodPost, "/cart", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	select {
	case s := <-recv:
		if s.Service != "checkout-api" {
			t.Errorf("Service = %q", s.Service)
		}
		if s.Method != http.MethodPost {
			t.Errorf("Method = %q", s.Method)
		}
		if s.Path != "/cart" {
			t.Errorf("Path = %q", s.Path)
		}
		if s.Status != http.StatusCreated {
			t.Errorf("Status = %d", s.Status)
		}
		if s.DurationMS < 5 {
			t.Errorf("DurationMS = %v, want >= 5", s.DurationMS)
		}
		if s.TraceID == "" || s.SpanID == "" {
			t.Errorf("expected generated TraceID/SpanID, got %q/%q", s.TraceID, s.SpanID)
		}
		if s.ParentID != "" {
			t.Errorf("ParentID = %q, want empty for a root span", s.ParentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for span over UDP")
	}
}

func TestMiddleware_WithQueryString_CapturesRawQuery(t *testing.T) {
	addr, recv := startUDPCollector(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	wrapped := Middleware(inner, WithAgentAddr(addr), WithQueryString())

	req := httptest.NewRequest(http.MethodGet, "/search?page=2&sort=price", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	select {
	case s := <-recv:
		if s.Path != "/search" {
			t.Errorf("Path = %q, want /search (unaffected by query capture)", s.Path)
		}
		if got := s.Tags["query_string"]; got != "page=2&sort=price" {
			t.Errorf("Tags[query_string] = %q, want %q", got, "page=2&sort=price")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for span over UDP")
	}
}

func TestMiddleware_WithoutOption_QueryStringNotCaptured(t *testing.T) {
	addr, recv := startUDPCollector(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	wrapped := Middleware(inner, WithAgentAddr(addr))

	req := httptest.NewRequest(http.MethodGet, "/search?page=2", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	select {
	case s := <-recv:
		if _, ok := s.Tags["query_string"]; ok {
			t.Errorf("Tags[query_string] = %q, want absent (option not set)", s.Tags["query_string"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for span over UDP")
	}
}

func TestMiddleware_WithQueryString_NoQueryStringPresent_TagAbsent(t *testing.T) {
	addr, recv := startUDPCollector(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	wrapped := Middleware(inner, WithAgentAddr(addr), WithQueryString())

	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	select {
	case s := <-recv:
		if _, ok := s.Tags["query_string"]; ok {
			t.Errorf("Tags[query_string] = %q, want absent (no query string on request)", s.Tags["query_string"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for span over UDP")
	}
}

func TestMiddleware_WithQueryString_RedactsSensitiveValues(t *testing.T) {
	addr, recv := startUDPCollector(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	wrapped := Middleware(inner, WithAgentAddr(addr), WithQueryString())

	req := httptest.NewRequest(http.MethodGet, "/search?page=2&token=abc123", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	select {
	case s := <-recv:
		want := "page=2&token=<redacted>"
		if got := s.Tags["query_string"]; got != want {
			t.Errorf("Tags[query_string] = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for span over UDP")
	}
}

func TestMiddleware_DefaultStatusIsOKWhenHandlerNeverCallsWriteHeader(t *testing.T) {
	addr, recv := startUDPCollector(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok")) // implicit 200, no explicit WriteHeader call
	})
	wrapped := Middleware(inner, WithAgentAddr(addr))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	select {
	case s := <-recv:
		if s.Status != http.StatusOK {
			t.Errorf("Status = %d, want 200", s.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for span over UDP")
	}
}
