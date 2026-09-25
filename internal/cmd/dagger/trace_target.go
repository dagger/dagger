package daggercmd

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"go.opentelemetry.io/otel/trace"
)

// spanSelector addresses a single span within a trace by exactly one of a raw
// span ID, a check name, or a test name. The zero value addresses nothing,
// which callers read as "the whole trace" (its root span). It backs the
// --span/--check/--test flags of 'dagger cloud traces view', so a human can
// name a check or test instead of copying an opaque span hex.
//
// Checks and tests roll up their subtree's logs; a raw span rolls up only with
// descendants (--descendants).
type spanSelector struct {
	span        string
	check       string
	test        string
	descendants bool
}

// isSet reports whether any selector was given (vs. the zero "whole trace").
func (s spanSelector) isSet() bool {
	return s.span != "" || s.check != "" || s.test != ""
}

// title names the selection for the log pager.
func (s spanSelector) title(traceID string) string {
	switch {
	case s.check != "":
		return s.check
	case s.test != "":
		return s.test
	case s.span != "":
		return "span " + s.span
	default:
		return "trace " + traceID
	}
}

func parseSpanID(hex string) (dagui.SpanID, error) {
	sid, err := trace.SpanIDFromHex(hex)
	if err != nil {
		return dagui.SpanID{}, fmt.Errorf("invalid span %q: %w", hex, err)
	}
	return dagui.SpanID{SpanID: sid}, nil
}

func (s spanSelector) validate() error {
	n := 0
	for _, v := range []string{s.span, s.check, s.test} {
		if v != "" {
			n++
		}
	}
	if n > 1 {
		return fmt.Errorf("--span, --check, and --test are mutually exclusive")
	}
	if s.descendants && s.span == "" {
		return fmt.Errorf("--descendants needs --span; --check and --test always include the descendants' logs")
	}
	return nil
}

// resolveSpan turns the selector into a concrete span ID plus whether to roll up
// descendant logs, when there is no frontend to resolve names against
// (resolveTraceTarget). A raw --span needs no lookup; --check/--test and the
// empty "whole trace" selector resolve against the trace's priority spans --
// checks and tests are priority spans, so they're present without fetching the
// whole trace. The empty selector resolves to the root span with descendants,
// i.e. the entire trace.
func (s spanSelector) resolveSpan(ctx context.Context, client *cloudapi.OTLPClient, traceID string) (spanID string, descendants bool, err error) {
	if s.span != "" {
		return s.span, s.descendants, nil
	}

	db, err := fetchPrioritySpans(ctx, client, traceID)
	if err != nil {
		return "", false, err
	}

	switch {
	case s.check != "":
		if span := db.FindCheckSpan(s.check); span != nil {
			return span.ID.String(), true, nil
		}
		return "", false, fmt.Errorf("no check named %q in trace %s", s.check, traceID)
	case s.test != "":
		if span := db.FindTestSpan(s.test); span != nil {
			return span.ID.String(), true, nil
		}
		return "", false, fmt.Errorf("no test named %q in trace %s", s.test, traceID)
	default:
		if db.RootSpan != nil {
			return db.RootSpan.ID.String(), true, nil
		}
		return "", false, fmt.Errorf("no root span found in trace %s (no data yet?)", traceID)
	}
}

// fetchPrioritySpans loads a trace's priority (root) spans into a private DB
// via the incremental selection. For a completed trace the stream delivers
// the priority set and returns.
func fetchPrioritySpans(ctx context.Context, client *cloudapi.OTLPClient, traceID string) (*dagui.DB, error) {
	db := dagui.NewDB()
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: db})
	importer.KeepRoots = true
	if err := client.FetchSpans(ctx, traceID, cloudapi.SpanSelection{
		Incremental: true,
	}, importer.ImportSpans); err != nil {
		return nil, fmt.Errorf("fetch trace spans: %w", err)
	}
	return db, nil
}
