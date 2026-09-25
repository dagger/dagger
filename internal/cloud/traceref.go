package cloud

import (
	"errors"
	"net/url"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// TraceRef addresses a trace, and optionally the org and span that a Dagger
// Cloud link names.
type TraceRef struct {
	TraceID string
	Org     string
	SpanID  string
}

// ParseTraceRef reads a trace ID, a pasted 'dagger cloud traces view <id>' or
// 'dagger trace <id>' command, or a https://dagger.cloud/<org>/traces/<id>
// link, which can select a span with ?span=<id>.
//
// Links are identifiers only: requests always go to the configured Cloud API,
// never to a host from the input. Other hosts, arbitrary URLs and shell syntax
// are rejected, and the error does not repeat the input.
func ParseTraceRef(s string) (TraceRef, error) {
	s = strings.TrimSpace(s)
	switch fields := strings.Fields(s); {
	case len(fields) == 3 && slices.Equal(fields[:2], []string{"dagger", "trace"}):
		s = fields[2]
	case len(fields) == 5 && slices.Equal(fields[:4], []string{"dagger", "cloud", "traces", "view"}):
		s = fields[4]
	}
	var ref TraceRef
	if u, err := url.Parse(s); err == nil && u.Scheme == "https" && u.Host == "dagger.cloud" && u.User == nil {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i := range parts {
			if parts[i] == "traces" && i+1 < len(parts) {
				s = parts[i+1]
				if i > 0 {
					ref.Org = parts[i-1]
				}
				break
			}
		}
		ref.SpanID = u.Query().Get("span")
	}
	id, err := trace.TraceIDFromHex(strings.ToLower(s))
	if err != nil {
		return TraceRef{}, errors.New("invalid trace: pass a 32-character hex trace ID, 'dagger cloud traces view <id>', or a https://dagger.cloud/<org>/traces/<id> URL")
	}
	ref.TraceID = id.String()
	return ref, nil
}
