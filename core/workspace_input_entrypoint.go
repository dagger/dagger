package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/hashutil"
)

func CaptureEntrypointSourceInputs(
	ctx context.Context,
	src *ModuleSource,
	workspace dagql.ObjectResult[*Workspace],
	call *FunctionCall,
) (context.Context, func(dagql.ObjectResult[*Directory]) (dagql.ObjectResult[*Directory], error)) {
	reads := &workspaceInputReads{workspace: workspace.Self()}
	call.workspaceInputs = reads
	ctx = context.WithValue(ctx, workspaceInputReadsKey{}, reads)
	ctx = context.WithValue(ctx, workspaceInputSourceContextKey{}, struct{}{})
	return ctx, func(sources dagql.ObjectResult[*Directory]) (dagql.ObjectResult[*Directory], error) {
		workspaceDigest, err := workspace.ContentPreferredDigest(ctx)
		if err != nil {
			return sources, err
		}
		codeDigest, err := src.ContextDirectory.ContentPreferredDigest(ctx)
		if err != nil {
			return sources, err
		}
		key := hashutil.HashStrings("entrypoint-source-inputs/v1", workspaceInputKey(workspace.Self()), workspaceDigest.String(), codeDigest.String(), src.SourceRootSubpath, src.Entrypoint.Source, src.DangVersion).String()
		summary := reads.snapshot()
		collector, err := newWorkspaceInputCollector(ctx)
		if err != nil {
			return sources, err
		}
		collector.retainedField(sources, "returned sources")
		summary.merge(collector.result)
		summary.Sources = append(summary.Sources, workspaceInputSource{Workspace: workspaceInputKey(workspace.Self()), Captures: []string{key}})
		captured, err := publishWorkspaceInputDirectory(ctx, sources, summary, key)
		if err != nil {
			return sources, err
		}
		src.EntrypointInputsKey = key
		return captured, nil
	}
}

// CaptureWorkspaceInputDirectory preserves source inputs across native assembly.
func CaptureWorkspaceInputDirectory(ctx context.Context, directory dagql.ObjectResult[*Directory], inputs ...dagql.AnyResult) (dagql.ObjectResult[*Directory], error) {
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return directory, err
	}
	for _, input := range inputs {
		collector.retainedField(input, "prepared source")
	}
	summary := collector.result.normalized()
	encoded, _ := json.Marshal(summary)
	return publishWorkspaceInputDirectory(ctx, directory, summary, hashutil.HashStrings("prepared-source-inputs/v1", string(encoded)).String())
}

func publishWorkspaceInputDirectory(ctx context.Context, sources dagql.ObjectResult[*Directory], summary workspaceInputSummary, key string) (dagql.ObjectResult[*Directory], error) {
	id, err := sources.ID()
	if err != nil {
		return sources, err
	}
	request := &dagql.CallRequest{
		ResultCall: &dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "entrypoint_source_inputs",
			Type:        dagql.NewResultCallType(sources.Type()),
			Receiver:    &dagql.ResultCallRef{ResultID: id.EngineResultID()},
			Args: []*dagql.ResultCallArg{{
				Name:  "capture",
				Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: key},
			}},
		},
		IsPersistable:       true,
		ResultFactsPrepared: true,
	}
	appendWorkspaceInputFact(ctx, request, summary.normalized(), true)
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return sources, err
	}
	server, err := CurrentDagqlServer(ctx)
	if err != nil {
		return sources, err
	}
	metadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return sources, err
	}
	result, err := cache.GetOrInitCall(ctx, metadata.SessionID, server, request, func(context.Context) (dagql.AnyResult, error) {
		return sources, nil
	})
	if err != nil {
		return sources, err
	}
	captured, ok := result.(dagql.ObjectResult[*Directory])
	if !ok {
		return sources, fmt.Errorf("entrypoint sources capture returned %T", result)
	}
	return captured, nil
}
