package daggercmd

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloudapi "github.com/dagger/dagger/internal/cloud"
)

// spanSelector addresses a single span within a trace by exactly one of a raw
// span ID, a check name, or a test name. The zero value addresses nothing,
// which callers read as "the whole trace" (its root span). It backs the
// --span/--check/--test flags shared by 'dagger trace' and 'dagger cloud logs',
// so a human can name a check or test instead of copying an opaque span hex.
type spanSelector struct {
	span  string
	check string
	test  string
}

// isSet reports whether any selector was given (vs. the zero "whole trace").
func (s spanSelector) isSet() bool {
	return s.span != "" || s.check != "" || s.test != ""
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
	return nil
}

// resolveSpan turns the selector into a concrete span ID plus whether to roll up
// descendant logs, for 'dagger cloud logs'. A raw --span needs no lookup and
// stands alone (just that span); --check/--test and the empty "whole trace"
// selector resolve against the trace's priority spans -- checks and tests are
// priority spans, so they're present without fetching the whole trace -- and
// roll up their subtree. The empty selector resolves to the root span with
// descendants, i.e. the entire trace. ('dagger trace' loads the whole trace and
// resolves the same names against its frontend instead: resolveTraceTarget.)
func (s spanSelector) resolveSpan(ctx context.Context, client *cloudapi.OTLPClient, traceID string) (spanID string, descendants bool, err error) {
	if s.span != "" {
		return s.span, false, nil
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
