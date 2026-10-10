package core

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestWorkloadDemandFollowsCompletedDelegation(t *testing.T) {
	ctx, cache, srv, session := newContainerPartsTestCtx(t)
	recorded := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorded))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx = enginetel.WithWorkloadTracer(ctx, provider.Tracer(enginetel.WorkloadScope))
	baseOp := &containerPartsTestBaseOp{LazyState: NewLazyState()}
	base := &Container{FS: new(LazyAccessor[*Directory, *Container]), MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]), Lazy: baseOp}
	baseResult := attachContainerPartsTestResult(t, ctx, cache, srv, session, "workload-producer", base)
	parent := baseResult
	for _, name := range []string{"FIRST", "SECOND", "THIRD"} {
		child := &Container{FS: new(LazyAccessor[*Directory, *Container]), MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
			Lazy: &ContainerWithEnvVariableLazy{LazyState: NewLazyState(), Parent: parent, Name: name, Value: "set"}}
		parent = attachContainerPartsTestResult(t, ctx, cache, srv, session, "workload-"+name, child)
	}
	require.NoError(t, cache.EvaluateParts(ctx, parent, ContainerPartMetadata))
	require.Zero(t, baseOp.fsRuns.Load(), "metadata does not demand the filesystem")
	baseID, ok := dagql.CacheResultNumber(baseResult)
	require.True(t, ok)
	attributes := func(span sdktrace.ReadOnlySpan) map[string]string {
		attrs := map[string]string{}
		for _, attr := range span.Attributes() {
			attrs[string(attr.Key)] = attr.Value.Emit()
		}
		return attrs
	}
	beforeFS := len(recorded.Ended())
	require.NoError(t, cache.EvaluateParts(ctx, parent, ContainerPartFS))
	require.Equal(t, int32(1), baseOp.fsRuns.Load())
	var fsScope string
	for _, span := range recorded.Ended()[beforeFS:] {
		attrs := attributes(span)
		if attrs[telemetryattrs.CacheOutcomeAttr] == "executed" && attrs[telemetryattrs.CacheResultIDAttr] == strconv.FormatUint(baseID, 10) {
			fsScope = attrs[telemetryattrs.WorkloadScopeAttr]
		}
	}
	require.NotEmpty(t, fsScope)
	before := len(recorded.Ended())
	require.NoError(t, cache.EvaluateParts(ctx, parent, ContainerPartFS))
	require.Equal(t, int32(1), baseOp.fsRuns.Load(), "reuse does not run the producer")
	var producers []string
	for _, span := range recorded.Ended()[before:] {
		attrs := attributes(span)
		if attrs[telemetryattrs.CacheOutcomeAttr] == "hit" && attrs[telemetryattrs.WorkloadScopeAttr] == fsScope {
			producers = append(producers, attrs[telemetryattrs.CacheResultIDAttr])
		}
	}
	require.Equal(t, []string{strconv.FormatUint(baseID, 10)}, producers, "the use belongs to the original producer, not to each copied result")
}

func TestWorkloadWholeOperationKeepsItsRestoredGroup(t *testing.T) {
	for _, field := range []string{"_builtinContainer", "import"} {
		t.Run(field, func(t *testing.T) {
			frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field,
				Type: dagql.NewResultCallType((&Container{}).Type())}
			var operation Lazy[*Container] = &ContainerBuiltinLazy{LazyState: NewLazyState()}
			if field == "import" {
				operation = &ContainerImportLazy{LazyState: NewLazyState()}
			}
			native := &Container{Lazy: operation}
			restored := &Container{Lazy: &ContainerRestoreLazy{}, lazyJSON: json.RawMessage(`{}`)}
			for _, part := range []dagql.PartKey{ContainerPartMetadata, ContainerPartFS, ContainerPartExecMeta} {
				groups, err := native.WorkloadEvaluationGroups(t.Context(), frame, []dagql.PartKey{part})
				require.NoError(t, err)
				require.Equal(t, []dagql.LazyGroupKey{dagql.LazyGroupWhole}, groups)
				restoredGroups, err := restored.WorkloadEvaluationGroups(t.Context(), frame, []dagql.PartKey{part})
				require.NoError(t, err)
				require.Equal(t, groups, restoredGroups, "restored demand must use the producer's cost scope")
				storedGroup, err := restored.WorkloadEvaluationGroup(t.Context(), frame, dagql.LazyGroupKey(containerStoredOpenPrefix+string(part)))
				require.NoError(t, err)
				require.Equal(t, dagql.LazyGroupWhole, storedGroup)
				if part == ContainerPartMetadata {
					metadataGroup, err := restored.WorkloadEvaluationGroup(t.Context(), frame, ContainerLazyGroupMetadata)
					require.NoError(t, err)
					require.Equal(t, dagql.LazyGroupWhole, metadataGroup)
				}
			}
		})
	}
}

func TestWorkloadDelegationUsesThePartProducer(t *testing.T) {
	for _, test := range []struct {
		name, field, recipe string
		delegated           []dagql.LazyGroupKey
		produced            []dagql.LazyGroupKey
	}{
		{
			name: "metadata mutation", field: "withWorkdir", recipe: `{"parentResultID":42,"workdir":"/work"}`,
			delegated: []dagql.LazyGroupKey{"fs", "execMeta", "mount:/read", "mount:/write"},
			produced:  []dagql.LazyGroupKey{"metadata"},
		},
		{
			name: "joint command output", field: "withExec", recipe: `{"parentResultID":42}`,
			delegated: []dagql.LazyGroupKey{"mount:/read"},
			produced:  []dagql.LazyGroupKey{"metadata", "execOutputs", "fs", "execMeta", "mount:/write"},
		},
		{
			name: "mounted writer", field: "withMountedDirectory", recipe: `{"parentResultID":42,"target":"/write"}`,
			delegated: []dagql.LazyGroupKey{"fs", "execMeta", "mount:/read"},
			produced:  []dagql.LazyGroupKey{"metadata", "write", "mount:/write"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := persistedContainerPayload{
				LazyJSON: json.RawMessage(test.recipe),
				Metadata: persistedContainerMetadata{Consumed: true, Value: persistedContainerMetadataValue{
					Platform: Platform{OS: "linux", Architecture: "amd64"},
					Mounts: []persistedContainerMountPayload{
						{Target: "/read", Kind: persistedContainerMountKindDirectory, Readonly: true},
						{Target: "/write", Kind: persistedContainerMountKindDirectory},
					},
				}},
			}
			raw, err := json.Marshal(payload)
			require.NoError(t, err)
			visit := dagql.PersistedPayloadVisit{
				Call: &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: test.field,
					Type: dagql.NewResultCallType((&Container{}).Type()), Receiver: &dagql.ResultCallRef{ResultID: 42}},
				Payload: raw,
			}
			for _, group := range test.delegated {
				owner, err := foreignFamilyCodec("Container").WorkloadPartDelegation(visit, group)
				require.NoError(t, err)
				require.Equal(t, &dagql.PartDelegation{ParentResultID: 42, Address: dagql.PersistedPartAddress{Part: dagql.PartKey(group)}}, owner)
			}
			for _, group := range test.produced {
				owner, err := foreignFamilyCodec("Container").WorkloadPartDelegation(visit, group)
				require.NoError(t, err)
				require.Nil(t, owner, "a group's own output must not use a parent's identity")
			}
		})
	}
}
