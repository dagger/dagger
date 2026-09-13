package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/log"
)

// Report only explicitly resolved install/update sources. Dependency and SDK
// loads use moduleSource directly and must not add more command output.
func reportWorkspaceModuleResolution(ctx context.Context, original string, src *core.ModuleSource) {
	line := workspaceModuleResolutionLine(original, src)
	if line == "" {
		return
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return
	}
	// The session is one CLI command. A repeated materialization or load must
	// not print the same source again. Reporting needs no remote request.
	_, _ = cache.GetOrInitArbitrary(ctx, md.SessionID,
		"module-resolution-report:"+md.SessionID+":"+original,
		func(ctx context.Context) (any, error) {
			writer := telemetry.GlobalWriter(ctx, core.InstrumentationLibrary,
				log.Int(telemetry.StdioStreamAttr, 2))
			_, _ = fmt.Fprintln(writer, line)
			return true, nil
		})
}

func workspaceModuleResolutionLine(original string, src *core.ModuleSource) string {
	if src == nil || src.Git == nil || src.Git.ResolvedCloneRef == "" || src.Git.Version == "" {
		return ""
	}
	resolved := gitref.GitURLRefString(src.Git.ResolvedCloneRef, src.SourceRootSubpath, src.Git.Version)
	return gitref.DisplayRef(original) + " ➡️ " + gitref.DisplayRef(resolved)
}
