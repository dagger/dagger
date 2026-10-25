package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Multi-span log reads. A frontend that renders only some spans reads their
// display text on demand, for many spans at once, so the selection travels in
// a POST body rather than a query string. The same body is accepted by a
// client's own live store (/v1/logs/query) and by a retained archive
// (/v1/telemetry/archives/{trace}/logs).
//
// Display text is a log record with a text body that is neither a semantic
// record (call payload, span name, progress, agent state) nor an agent
// control record. Unlike LogRecordsLogs it includes global and verbose
// records: the frontend, not the server, decides what to show.

const (
	// LogQueryContentType is the request body type of log queries and
	// searches.
	LogQueryContentType = "application/json"
	// MaxLogQuerySpans bounds the spans one log query or search names.
	MaxLogQuerySpans = 8192
	// MaxLogQueryBytes bounds a log query or search body.
	MaxLogQueryBytes = 2 << 20
	// MaxLogQueryDigests bounds the digests one span of a query names.
	MaxLogQueryDigests = 64
)

// LogSpan names one span whose display text to read.
type LogSpan struct {
	SpanID string `json:"span_id"`
	// Descendants also selects the span's subtree, following the same
	// containment (child edges and cause links) as LogSelection.
	Descendants bool `json:"descendants,omitempty"`
	// Digests also selects rows carrying one of these dagger.io/dag.digest
	// values wherever they were emitted: dagui routes such a row (e.g. a
	// service's stdio) to the span that created the digest.
	Digests []string `json:"digests,omitempty"`
}

// LogQuery selects the display text of a set of spans. The server may return
// more than the selection (e.g. rows of spans whose position in the tree it
// does not know yet); the reader applies the frontend's exact routing.
type LogQuery struct {
	Spans []LogSpan `json:"spans"`
	// Follow keeps a live read open past the store's current end until the
	// client cancels or the session's telemetry drains. A one-shot read ends
	// at the end the store had when the request arrived. Archives are
	// finite, so it is ignored there.
	Follow bool `json:"follow,omitempty"`
}

// Validate checks q against the shared client/server contract.
func (q LogQuery) Validate() error {
	if len(q.Spans) == 0 {
		return errors.New("log query selects no spans")
	}
	return validateLogSpans(q.Spans)
}

func validateLogSpans(spans []LogSpan) error {
	if len(spans) > MaxLogQuerySpans {
		return fmt.Errorf("log query selects %d spans, more than %d", len(spans), MaxLogQuerySpans)
	}
	for _, s := range spans {
		if err := validSelectionSpanID(s.SpanID); err != nil {
			return err
		}
		if len(s.Digests) > MaxLogQueryDigests {
			return fmt.Errorf("span %s selects %d digests, more than %d", s.SpanID, len(s.Digests), MaxLogQueryDigests)
		}
		for _, d := range s.Digests {
			if d == "" {
				return fmt.Errorf("span %s selects an empty digest", s.SpanID)
			}
		}
	}
	return nil
}

// LogSearch searches display text for a pattern, optionally within a set of
// spans (each optionally with descendants). Matching is per line.
type LogSearch struct {
	Pattern string `json:"pattern"`
	// Regexp interprets Pattern as an RE2 regular expression instead of a
	// literal substring.
	Regexp bool `json:"regexp,omitempty"`
	// IgnoreCase matches case-insensitively.
	IgnoreCase bool `json:"ignore_case,omitempty"`
	// Spans limits the search; empty searches every span.
	Spans []LogSpan `json:"spans,omitempty"`
	// After resumes a search after this log row (LogSearchResult.Next).
	After int64 `json:"after,omitempty"`
	// Limit bounds the matches returned; zero means DefaultLogSearchLimit.
	Limit int `json:"limit,omitempty"`
}

const (
	DefaultLogSearchLimit = 100
	MaxLogSearchLimit     = 1000
	// MaxLogSearchSnippet bounds a match's text, in bytes.
	MaxLogSearchSnippet = 256
)

// Matcher compiles s into a line predicate, after validating it.
func (s LogSearch) Matcher() (func(string) bool, error) {
	if s.Pattern == "" {
		return nil, errors.New("log search has no pattern")
	}
	if s.After < 0 || s.Limit < 0 || s.Limit > MaxLogSearchLimit {
		return nil, errors.New("invalid log search bounds")
	}
	if err := validateLogSpans(s.Spans); err != nil {
		return nil, err
	}
	pattern := s.Pattern
	if !s.Regexp {
		if !s.IgnoreCase {
			return func(line string) bool { return strings.Contains(line, pattern) }, nil
		}
		pattern = regexp.QuoteMeta(pattern)
	}
	if s.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid log search pattern: %w", err)
	}
	return re.MatchString, nil
}

// LogMatch locates one matching line.
type LogMatch struct {
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
	// Digest is the row's dagger.io/dag.digest, for rows dagui routes to
	// the digest's creator span instead of SpanID.
	Digest string `json:"digest,omitempty"`
	// Row is the log row ID (the stream cursor of the row); Line the
	// zero-based line within its body.
	Row  int64 `json:"row"`
	Line int   `json:"line"`
	// Text is the matching line, truncated to MaxLogSearchSnippet bytes.
	Text string `json:"text"`
}

// LogSearchResult is one page of search results. Next is the last row the
// search scanned; More reports that it stopped before the end of the store
// (at its match limit or its scan budget), so another search After Next
// continues it.
type LogSearchResult struct {
	Matches []LogMatch `json:"matches"`
	Next    int64      `json:"next"`
	More    bool       `json:"more,omitempty"`
}

// DecodeLogBody reads a bounded JSON request body into v.
func DecodeLogBody(r io.Reader, v any) error {
	data, err := io.ReadAll(io.LimitReader(r, MaxLogQueryBytes+1))
	if err != nil {
		return fmt.Errorf("read log query: %w", err)
	}
	if len(data) > MaxLogQueryBytes {
		return fmt.Errorf("log query body exceeds %d bytes", MaxLogQueryBytes)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode log query: %w", err)
	}
	return nil
}
