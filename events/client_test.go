package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testSocketPath returns a short path under /tmp rather than t.TempDir()
// (which on macOS lives under a long /var/folders/... path that can exceed
// the ~104-byte AF_UNIX sun_path limit).
func testSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "miniargus-sdk-events-test")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, fmt.Sprintf("agent-%d.sock", os.Getpid()))
}

// startFakeAgent listens on socketPath and returns a channel of decoded
// events from every accepted connection.
func startFakeAgent(t *testing.T, socketPath string) <-chan Event {
	t.Helper()
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	ch := make(chan Event, 100)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					var e Event
					if json.Unmarshal(scanner.Bytes(), &e) == nil {
						ch <- e
					}
				}
			}()
		}
	}()
	return ch
}

func TestClient_EmitDeliversToAgent(t *testing.T) {
	socketPath := testSocketPath(t)
	received := startFakeAgent(t, socketPath)

	c := NewClient(socketPath)
	defer c.Close()

	c.Emit(Event{Name: "user.signup", Tags: map[string]string{"plan": "free"}})

	select {
	case e := <-received:
		if e.Name != "user.signup" {
			t.Errorf("Name = %q", e.Name)
		}
		if e.Tags["plan"] != "free" {
			t.Errorf("Tags[plan] = %q", e.Tags["plan"])
		}
		if e.TS.IsZero() {
			t.Error("expected TS to be auto-filled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event delivery")
	}
}

func TestClient_EmitDoesNotBlockWhenAgentIsDown(t *testing.T) {
	// No listener at all -- socketPath doesn't exist.
	c := NewClient(filepath.Join(t.TempDir(), "no-such-agent.sock"))
	defer c.Close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			c.Emit(Event{Name: "x"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Emit blocked with no agent listening")
	}
}

func TestClient_MultipleEventsOnPersistentConnection(t *testing.T) {
	socketPath := testSocketPath(t)
	received := startFakeAgent(t, socketPath)

	c := NewClient(socketPath)
	defer c.Close()

	for _, name := range []string{"a", "b", "c"} {
		c.Emit(Event{Name: name})
	}

	got := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case e := <-received:
			got[e.Name] = true
		case <-deadline:
			t.Fatalf("only received %v before timeout", got)
		}
	}
}
