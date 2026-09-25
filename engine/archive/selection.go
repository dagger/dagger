package archive

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// SpanSelection selects the priority view and/or listened subtrees of a fixed
// archive cut. Nil in StreamOptions preserves the unfiltered history stream.
// Unlike Cloud, archives are finite: no time-based follow/backfill is needed.
type SpanSelection struct {
	// Full bypasses priority filtering while retaining optional UI annotations.
	Full      bool
	NoRoot    bool
	Listen    []string
	DagUIView bool
}

const (
	LogRecordsAll          = ""
	LogRecordsLogs         = "logs"
	LogRecordsCallPayloads = "call_payloads"
	LogRecordsMetadata     = "metadata"
)

// LogSelection selects a span's own or descendant records. Records selects
// ordinary output (including media), call payloads, or all historical records.
// Control records remain exclusive to the verified bootstrap in every mode.
type LogSelection struct {
	SpanID      string
	Descendants bool
	After       *time.Time
	Records     string
}

func (s StreamOptions) SelectionQuery(signal string) (url.Values, error) {
	q := make(url.Values)
	if s.Spans != nil {
		if signal != "traces" {
			return nil, fmt.Errorf("span selection requires traces")
		}
		q.Set("root", strconv.FormatBool(!s.Spans.NoRoot))
		if s.Spans.Full {
			q.Set("full", "true")
		}
		for _, id := range s.Spans.Listen {
			q.Add("listen", id)
		}
		if s.Spans.DagUIView {
			q.Set("view", "dagui")
		}
	}
	if s.Logs != nil {
		if signal != "logs" {
			return nil, fmt.Errorf("log selection requires logs")
		}
		q.Set("span_id", s.Logs.SpanID)
		q.Set("descendants", strconv.FormatBool(s.Logs.Descendants))
		q.Set("records", s.Logs.Records)
		if s.Logs.After != nil {
			q.Set("after", s.Logs.After.UTC().Format(time.RFC3339Nano))
		}
	}
	_, _, err := ParseSelection(signal, q)
	return q, err
}

// ParseSelection validates the shared archive client/server query contract.
func ParseSelection(signal string, q url.Values) (*SpanSelection, *LogSelection, error) {
	var spans *SpanSelection
	var logs *LogSelection
	var err error
	if q.Has("root") || q.Has("listen") || q.Has("view") || q.Has("full") {
		if signal != "traces" {
			return nil, nil, fmt.Errorf("span selection requires traces")
		}
		spans, err = parseSpanSelection(q)
		if err != nil {
			return nil, nil, err
		}
	}
	if q.Has("span_id") || q.Has("descendants") || q.Has("records") || q.Has("after") {
		if signal != "logs" {
			return nil, nil, fmt.Errorf("log selection requires logs")
		}
		logs, err = parseLogSelection(q)
		if err != nil {
			return nil, nil, err
		}
	}
	return spans, logs, nil
}

func validSelectionSpanID(id string) error {
	if _, err := trace.SpanIDFromHex(id); err != nil {
		return fmt.Errorf("invalid span ID %q: %w", id, err)
	}
	return nil
}

func selectionBool(q url.Values, key string, def bool) (bool, error) {
	if !q.Has(key) {
		return def, nil
	}
	return strconv.ParseBool(q.Get(key))
}

func parseSpanSelection(q url.Values) (*SpanSelection, error) {
	root, err := selectionBool(q, "root", true)
	if err != nil {
		return nil, err
	}
	if v := q.Get("view"); v != "" && v != "dagui" {
		return nil, fmt.Errorf("invalid archive view %q", v)
	}
	if len(q["listen"]) > 256 {
		return nil, fmt.Errorf("too many listened spans")
	}
	for _, id := range q["listen"] {
		if err := validSelectionSpanID(id); err != nil {
			return nil, err
		}
	}
	full, err := selectionBool(q, "full", false)
	if err != nil {
		return nil, err
	}
	if full && (!root || len(q["listen"]) != 0) {
		return nil, fmt.Errorf("full view cannot select roots or subtrees")
	}
	return &SpanSelection{Full: full, NoRoot: !root, Listen: q["listen"], DagUIView: q.Get("view") == "dagui"}, nil
}

func parseLogSelection(q url.Values) (*LogSelection, error) {
	descendants, err := selectionBool(q, "descendants", false)
	if err != nil {
		return nil, err
	}
	logs := &LogSelection{SpanID: q.Get("span_id"), Descendants: descendants, Records: q.Get("records")}
	if logs.SpanID != "" {
		if err := validSelectionSpanID(logs.SpanID); err != nil {
			return nil, err
		}
	}
	if (descendants || logs.Records == LogRecordsLogs) && logs.SpanID == "" {
		return nil, fmt.Errorf("log selection requires a span ID")
	}
	switch logs.Records {
	case LogRecordsAll, LogRecordsLogs, LogRecordsCallPayloads, LogRecordsMetadata:
	default:
		return nil, fmt.Errorf("invalid log record class %q", logs.Records)
	}
	if q.Has("after") {
		t, err := time.Parse(time.RFC3339Nano, q.Get("after"))
		if err != nil {
			return nil, err
		}
		logs.After = &t
	}
	return logs, nil
}
