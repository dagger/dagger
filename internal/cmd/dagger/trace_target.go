package daggercmd

import (
	"context"
	"fmt"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	telemetry "github.com/dagger/otel-go"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
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
// descendant logs. A raw --span needs no lookup and stands alone (just that
// span); --check/--test and the empty "whole trace" selector resolve against the
// trace's priority spans -- the same set 'dagger trace --full' loads first, so
// checks and tests (priority spans) are present without fetching the whole
// trace -- and roll up their subtree. The empty selector resolves to the root
// span with descendants, i.e. the entire trace.
func (s spanSelector) resolveSpan(ctx context.Context, client *cloudapi.Client, orgID, traceID string) (spanID string, descendants bool, err error) {
	if s.span != "" {
		return s.span, false, nil
	}

	spans, err := fetchPrioritySpans(ctx, client, orgID, traceID)
	if err != nil {
		return "", false, err
	}

	switch {
	case s.check != "":
		for _, sp := range spans {
			if attrString(sp, telemetry.CheckNameAttr) == s.check {
				return sp.ID, true, nil
			}
		}
		return "", false, fmt.Errorf("no check named %q in trace %s", s.check, traceID)
	case s.test != "":
		if id := matchTestSpan(spans, s.test); id != "" {
			return id, true, nil
		}
		return "", false, fmt.Errorf("no test named %q in trace %s", s.test, traceID)
	default:
		for _, sp := range spans {
			if sp.ParentID == nil {
				return sp.ID, true, nil
			}
		}
		return "", false, fmt.Errorf("no root span found in trace %s (no data yet?)", traceID)
	}
}

// fetchPrioritySpans collects a trace's priority (root) spans via the same
// incremental subscription the --full loader uses. For a completed trace the
// stream delivers the priority set and returns.
//
// NOTE: 'dagger trace --full' already loads these spans through its frontend
// loader; resolving a --check/--test there re-fetches them. Cheap (~one batch),
// but worth deduping if the loader ever exposes its spans.
func fetchPrioritySpans(ctx context.Context, client *cloudapi.Client, orgID, traceID string) ([]cloudapi.SpanData, error) {
	var all []cloudapi.SpanData
	err := client.StreamSpansWith(ctx, orgID, traceID, cloudapi.SpanStreamOpts{
		Root:        true,
		Incremental: true,
	}, func(spans []cloudapi.SpanData) {
		all = append(all, spans...)
	})
	if err != nil {
		return nil, fmt.Errorf("fetch trace spans: %w", err)
	}
	return all, nil
}

// matchTestSpan finds a span ID for a test by name, matching the OTel test case
// name, an optional "<suite> <case>" qualification, or the span name. When
// several cases share a name it prefers a failed one, so the hint a failing
// report prints resolves to the failure the user is chasing.
func matchTestSpan(spans []cloudapi.SpanData, name string) string {
	var fallback string
	for _, sp := range spans {
		caseName := attrString(sp, string(semconv.TestCaseNameKey))
		if caseName == "" {
			continue
		}
		suite := attrString(sp, string(semconv.TestSuiteNameKey))
		if caseName != name &&
			suite+" "+caseName != name &&
			sp.Name != name {
			continue
		}
		if sp.Status.Code == "STATUS_CODE_ERROR" {
			return sp.ID
		}
		if fallback == "" {
			fallback = sp.ID
		}
	}
	return fallback
}

func attrString(sp cloudapi.SpanData, key string) string {
	if v, ok := sp.Attributes[key].(string); ok {
		return v
	}
	return ""
}
