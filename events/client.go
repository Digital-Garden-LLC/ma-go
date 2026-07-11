// Package events provides the custom-event client for the miniargus agent:
// client.Emit(event), delivered to the agent over a local Unix socket.
package events

import (
	"encoding/json"
	"net"
	"sync"
	"time"
)

// Event is the on-the-wire shape sent to the agent. Field names mirror the
// ingestion API's EventRow JSON (tenant_id/host excluded: the SDK doesn't
// know its tenant, and the agent stamps its own hostname regardless of
// whatever's sent here).
type Event struct {
	Name    string            `json:"name"`
	TS      time.Time         `json:"ts,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"`
	Payload json.RawMessage   `json:"payload,omitempty"`
}

const (
	defaultQueueSize = 1000
	dialTimeout      = 2 * time.Second
	writeTimeout     = 2 * time.Second
)

// Client delivers events to the agent asynchronously: Emit never blocks the
// caller on the agent being slow or unreachable. Events queue in a bounded
// channel and are dropped (not buffered indefinitely) if the agent can't
// keep up -- the same drop-oldest-under-backpressure philosophy the agent
// itself uses for its ring buffers, applied here as drop-newest-when-full
// since there's no ring buffer on this side, just a channel.
type Client struct {
	socketPath string
	queue      chan []byte
	done       chan struct{}
	closeOnce  sync.Once

	mu   sync.Mutex
	conn net.Conn
}

func NewClient(socketPath string) *Client {
	c := &Client{
		socketPath: socketPath,
		queue:      make(chan []byte, defaultQueueSize),
		done:       make(chan struct{}),
	}
	go c.run()
	return c
}

// Emit queues e for delivery and returns immediately.
func (c *Client) Emit(e Event) {
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return
	}
	select {
	case c.queue <- payload:
	default:
		// Queue full: the agent is unreachable or too slow to keep up.
		// Drop rather than block the caller or grow unbounded.
	}
}

func (c *Client) run() {
	for {
		select {
		case <-c.done:
			return
		case payload := <-c.queue:
			c.send(payload)
		}
	}
}

func (c *Client) send(payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		conn, err := net.DialTimeout("unix", c.socketPath, dialTimeout)
		if err != nil {
			return
		}
		c.conn = conn
	}

	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := c.conn.Write(append(payload, '\n')); err != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// Close stops the delivery goroutine and closes the underlying connection,
// if any. Queued-but-undelivered events are discarded.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
			c.conn = nil
		}
		c.mu.Unlock()
	})
}
