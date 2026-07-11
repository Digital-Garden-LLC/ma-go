package tracing

import "time"

// span is the UDP wire format sent to the agent. Field names deliberately
// mirror the ingestion API's TraceRow JSON (tenant_id excluded -- the SDK
// never sets it, the api service stamps it) so the agent can decode a
// packet almost directly into the row it ships onward. This module has no
// dependency on the main miniargus module (see repo layout notes in
// SPEC.md), so the shape is duplicated here rather than imported.
type span struct {
	TS         time.Time         `json:"ts"`
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	ParentID   string            `json:"parent_id"`
	Service    string            `json:"service"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Status     uint16            `json:"status"`
	DurationMS float64           `json:"duration_ms"`
	Tags       map[string]string `json:"tags,omitempty"`
}
