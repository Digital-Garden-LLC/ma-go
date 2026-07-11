package tracing

import "time"

// span is the UDP wire format sent to the agent. Field names deliberately
// mirror miniargus's ingestion API's TraceRow JSON (tenant_id excluded --
// the SDK never sets it, the api service stamps it) so the agent can
// decode a packet almost directly into the row it ships onward. This
// module has no dependency on miniargus (it's a separate, public repo --
// see this repo's README), so the shape is duplicated here rather than
// imported.
type span struct {
	TS       time.Time `json:"ts"`
	TraceID  string    `json:"trace_id"`
	SpanID   string    `json:"span_id"`
	ParentID string    `json:"parent_id"`
	Service  string    `json:"service"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Status   uint16    `json:"status"`
	// Name is a generic operation name for a non-HTTP child span (e.g.
	// "db.query", set via StartSpan) -- empty for the root HTTP span,
	// which is already fully identified by Method/Path/Status.
	Name       string            `json:"name"`
	DurationMS float64           `json:"duration_ms"`
	Tags       map[string]string `json:"tags,omitempty"`
}
