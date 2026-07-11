package tracing

import (
	"crypto/rand"
	"encoding/hex"
)

// newID returns n random bytes as lowercase hex. Trace/span IDs are raw hex
// (16 bytes for a trace ID, 8 for a span ID per the W3C Trace Context /
// OTel convention) -- deliberately not google/uuid formatted, since a UUID's
// dashes wouldn't match what a real OTel SDK emits and would break
// cross-compatibility with the ingestion API's OTLP path.
func newID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
