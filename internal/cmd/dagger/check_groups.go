package daggercmd

import (
	"context"
	"errors"
	"strings"
	"sync"

	"dagger.io/dagger/core"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Collection items retain their own results beneath one named check. Reporting
// the parent gives Cloud and GitHub the same check path that Native CI uses.
type checkGroup struct {
	span      trace.Span
	mu        sync.Mutex
	remaining int
	failure   error
}

type checkGroupKey struct {
	workspace core.ID
	path      string
}

type checkGroups map[checkGroupKey]*checkGroup

func (item artifactExecutionItem) checkGroupKey() checkGroupKey {
	key := checkGroupKey{path: item.CheckGroup}
	if item.Workspace != nil {
		key.workspace = item.Workspace.ID
	}
	return key
}

func isMissingCheckGroupField(err error) bool {
	var httpErr *graphql.HTTPError
	if !errors.As(err, &httpErr) || len(httpErr.Response.Errors) != 1 {
		return false
	}
	gqlErr := httpErr.Response.Errors[0]
	return gqlErr != nil && len(gqlErr.Path) == 0 &&
		gqlErr.Extensions["code"] == "GRAPHQL_VALIDATION_FAILED" &&
		strings.HasPrefix(gqlErr.Message, `Cannot query field "__checkGroup" on type "Artifact".`)
}

func newCheckGroups(ctx context.Context, items []artifactExecutionItem) checkGroups {
	groups := checkGroups{}
	for _, item := range items {
		if item.CheckGroup == "" {
			continue
		}
		key := item.checkGroupKey()
		group := groups[key]
		if group == nil {
			_, span := Tracer().Start(ctx, item.CheckGroup, trace.WithAttributes(
				attribute.String(telemetry.CheckNameAttr, item.CheckGroup),
				attribute.Bool(telemetryattrs.CheckCollectionGroupAttr, true),
				attribute.Bool(telemetry.UIRollUpLogsAttr, true),
				attribute.Bool(telemetry.UIRollUpSpansAttr, true),
			))
			group = &checkGroup{span: span}
			groups[key] = group
		}
		group.remaining++
	}
	return groups
}

func (g *checkGroup) context(ctx context.Context) context.Context {
	return trace.ContextWithSpan(ctx, g.span)
}

func (g *checkGroup) finish(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.remaining == 0 {
		return
	}
	if err != nil && g.failure == nil {
		g.failure = err
	}
	g.remaining--
	if g.remaining == 0 {
		g.end()
	}
}

func (g *checkGroup) end() {
	g.remaining = 0
	g.span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, g.failure == nil))
	telemetry.EndWithCause(g.span, &g.failure)
}

func (groups checkGroups) close() {
	for _, group := range groups {
		group.mu.Lock()
		if group.remaining > 0 {
			// Fail-fast may cancel a queued item before its function is called.
			if group.failure == nil {
				group.failure = context.Canceled
			}
			group.end()
		}
		group.mu.Unlock()
	}
}
