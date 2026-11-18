package daggercmd

import (
	"context"
	"errors"
	"sync"
	"testing"

	"dagger.io/dagger/core"
	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCheckGroupCompletion(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"all pass", nil},
		{"one fails", errors.New("test failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
			_, span := provider.Tracer("test").Start(t.Context(), "collection")
			group := &checkGroup{span: span, remaining: 3}
			var jobs sync.WaitGroup
			for i := range 3 {
				jobs.Go(func() {
					var err error
					if i == 0 {
						err = tc.err
					}
					group.finish(err)
				})
			}
			jobs.Wait()
			checkGroups{{path: "collection"}: group}.close()
			ended := recorder.Ended()
			require.Len(t, ended, 1, "one collection produces one final summary")
			if tc.err != nil {
				require.Equal(t, codes.Error, ended[0].Status().Code)
			} else {
				require.Equal(t, codes.Ok, ended[0].Status().Code)
			}
		})
	}
}

func TestCheckGroupCancelledBeforeItemsRun(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	_, span := provider.Tracer("test").Start(t.Context(), "collection")
	group := &checkGroup{span: span, remaining: 3}
	groups := checkGroups{{path: "collection"}: group}
	groups.close()
	groups.close()
	group.finish(nil)
	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, codes.Error, ended[0].Status().Code)
}

func TestCheckGroupsKeepWorkspacesSeparate(t *testing.T) {
	first := artifactExecutionItem{CheckGroup: "app/cases/check", Workspace: &struct{ ID core.ID }{ID: "first"}}
	second := first
	second.Workspace = &struct{ ID core.ID }{ID: "second"}
	groups := newCheckGroups(t.Context(), []artifactExecutionItem{first, first, second, {}})
	defer groups.close()
	require.Len(t, groups, 2, "ordinary checks do not create a group")
	require.Equal(t, 2, groups[first.checkGroupKey()].remaining)
	require.Equal(t, 1, groups[second.checkGroupKey()].remaining)
}

func TestMissingCheckGroupField(t *testing.T) {
	missing := `Cannot query field "__checkGroup" on type "Artifact".`
	validation := &gqlerror.Error{Message: missing, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}
	require.True(t, isMissingCheckGroupField(&graphql.HTTPError{Response: graphql.Response{Errors: gqlerror.List{validation}}}))
	resolver := *validation
	resolver.Path = ast.Path{ast.PathName("node")}
	require.False(t, isMissingCheckGroupField(&graphql.HTTPError{Response: graphql.Response{Errors: gqlerror.List{&resolver}}}))
	require.False(t, isMissingCheckGroupField(errors.New(missing)))
}
