package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestModuleUpdateRegenerationPlan(t *testing.T) {
	staged := &stagedWorkspaceConfig{ConfigDir: ".", Config: &workspace.Config{
		SDKs: map[string]workspace.SDKEntry{"go": {Scopes: map[string]workspace.SDKScope{
			"api":   {IsModule: true},
			"web":   {IsModule: true, Clients: []string{"./api"}},
			"tests": {IsModule: true, Clients: []string{"./api", "./web"}},
			"other": {Clients: []string{"./unrelated"}},
		}}},
	}}
	selections, err := selectSDKModuleClientsForModuleSources(staged, []string{"api"})
	require.NoError(t, err)
	plan, err := planSDKModuleClientRegeneration(staged, selections)
	require.NoError(t, err)
	require.Len(t, plan.ordered, 2)
	require.Equal(t, "web", plan.ordered[0].path)
	require.Equal(t, "tests", plan.ordered[1].path)
	require.Nil(t, plan.parent["go:web"], "do not generate the unselected API scope")
	require.Equal(t, plan.ordered[0], plan.parent["go:tests"])

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx, root := provider.Tracer("test").Start(context.Background(), "update: api")
	defer root.End()
	var generated []string
	failure := errors.New("broken client")
	err = runSDKModuleClientRegeneration(ctx, plan, func(ctx context.Context, node *sdkModuleGraphScope) error {
		generated = append(generated, node.path)
		if node.path == "tests" {
			return failure
		}
		return nil
	})
	require.ErrorContains(t, err, `re-generate scope "tests": broken client`)
	require.ErrorIs(t, err, failure)
	require.Equal(t, []string{"web", "tests"}, generated)
	var names []string
	for _, span := range recorder.Ended() {
		names = append(names, span.Name())
		require.Equal(t, codes.Error, span.Status().Code)
	}
	require.Equal(t, []string{"re-generate: ./tests", "downstream clients", "re-generate: ./web", "downstream clients"}, names)
}

func TestModuleUpdateRegenerationGroup(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "no clients", true: "failed SDK"}[selected], func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
			ctx, root := provider.Tracer("test").Start(context.Background(), "update: api")
			defer root.End()
			var selections []sdkModuleClientSelection
			if selected {
				selections = []sdkModuleClientSelection{{sdkName: "missing", workspaceScope: "web", configScopePath: "web"}}
			}
			_, err := (&workspaceSchema{}).regenerateSDKModuleClients(ctx, dagql.ObjectResult[*core.Workspace]{}, &stagedWorkspaceConfig{ConfigDir: ".", Config: &workspace.Config{}}, selections)
			if !selected {
				require.NoError(t, err)
				require.Empty(t, recorder.Ended(), "no empty regeneration group")
				return
			}
			require.ErrorContains(t, err, `re-generate scope "web"`)
			var names []string
			for _, span := range recorder.Ended() {
				names = append(names, span.Name())
				require.Equal(t, codes.Error, span.Status().Code)
			}
			require.Equal(t, []string{"re-generate: ./web", "downstream clients", "re-generate"}, names)
		})
	}
}
