package tracing

import (
	"net/url"
	"regexp"
	"strings"
)

// maxQueryStringLen bounds the captured query_string tag -- matches
// miniargus's existing precedent for "a reasonable captured-string
// snippet length" (see the agent's Postgres query_samples check,
// defaultQuerySampleMaxLength = 2048).
const maxQueryStringLen = 2048

// sensitiveQueryKeyPattern matches query parameter names that commonly
// carry secrets -- passwords, tokens, API keys, session ids, auth headers
// passed as params, signed URLs. Mirrors the spirit of Datadog APM's
// DD_TRACE_OBFUSCATION_QUERY_STRING_REGEXP default: redact by key name,
// not a denylist of exact values, since the set of secrets an app might
// pass is unbounded but the *shape* of the parameter name that carries
// one is fairly predictable.
//
// This is client-side, best-effort minimization -- it reduces how much
// raw sensitive data ever leaves the process. It is NOT the safety
// boundary: miniargus's own ingestion API re-applies the identical
// redaction server-side (see that repo's api/internal/ingest package),
// since a tenant's app code importing this SDK isn't a trust boundary the
// platform can rely on -- a fork or an out-of-date version could skip
// this entirely.
var sensitiveQueryKeyPattern = regexp.MustCompile(`(?i)pass|pwd|secret|token|key|auth|session|credential|signature`)

// redactQueryString replaces the value of every key=value pair whose key
// looks sensitive with a fixed "<redacted>" placeholder, leaving
// everything else -- parameter order, encoding, bare flags with no value
// -- untouched, then truncates the result to maxQueryStringLen. Operates
// on the raw (still percent-encoded) string rather than parsing via
// url.ParseQuery/Values.Encode, which would reorder parameters
// alphabetically and re-escape them -- undesirable for a value whose only
// purpose is human-readable debugging context on a span.
func redactQueryString(raw string) string {
	pairs := strings.Split(raw, "&")
	for i, pair := range pairs {
		rawKey, _, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		key := rawKey
		if unescaped, err := url.QueryUnescape(rawKey); err == nil {
			key = unescaped
		}
		if sensitiveQueryKeyPattern.MatchString(key) {
			pairs[i] = rawKey + "=<redacted>"
		}
	}
	joined := strings.Join(pairs, "&")
	if len(joined) > maxQueryStringLen {
		return joined[:maxQueryStringLen]
	}
	return joined
}
