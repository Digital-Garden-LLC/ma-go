package tracing

import (
	"strings"
	"testing"
)

func TestRedactQueryString_LeavesNonSensitiveParamsUntouched(t *testing.T) {
	got := redactQueryString("page=2&sort=price")
	if got != "page=2&sort=price" {
		t.Errorf("redactQueryString = %q, want unchanged", got)
	}
}

func TestRedactQueryString_RedactsSensitiveKeys(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"token", "token=abc123", "token=<redacted>"},
		{"password", "password=hunter2", "password=<redacted>"},
		{"api_key", "api_key=xyz", "api_key=<redacted>"},
		{"secret", "secret=shh", "secret=<redacted>"},
		{"session_id", "session_id=sid123", "session_id=<redacted>"},
		{"mixed case", "Authorization=Bearer%20xyz", "Authorization=<redacted>"},
		{"signature", "signature=deadbeef", "signature=<redacted>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redactQueryString(c.raw); got != c.want {
				t.Errorf("redactQueryString(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestRedactQueryString_MixOfSensitiveAndNot(t *testing.T) {
	got := redactQueryString("page=2&token=abc123&sort=price")
	want := "page=2&token=<redacted>&sort=price"
	if got != want {
		t.Errorf("redactQueryString = %q, want %q", got, want)
	}
}

func TestRedactQueryString_LeavesBareFlagsUntouched(t *testing.T) {
	got := redactQueryString("debug&page=2")
	if got != "debug&page=2" {
		t.Errorf("redactQueryString = %q, want unchanged", got)
	}
}

func TestRedactQueryString_HandlesPercentEncodedKeys(t *testing.T) {
	// "api%5Fkey" decodes to "api_key", which should still match.
	got := redactQueryString("api%5Fkey=xyz")
	if got != "api%5Fkey=<redacted>" {
		t.Errorf("redactQueryString = %q, want the raw key preserved with a redacted value", got)
	}
}

func TestRedactQueryString_TruncatesAtMaxLength(t *testing.T) {
	long := strings.Repeat("a=1&", 1000)
	got := redactQueryString(long)
	if len(got) != maxQueryStringLen {
		t.Errorf("len(redactQueryString(...)) = %d, want %d", len(got), maxQueryStringLen)
	}
}
