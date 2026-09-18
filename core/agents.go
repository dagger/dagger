package core

import (
	"context"
	"github.com/dagger/dagger/engine/slog"
)

func (llm *LLM) WarnToolNameCollisions(ctx context.Context) {
	if llm == nil || llm.mcp == nil {
		return
	}
	collisions, err := llm.mcp.ToolNameCollisions(ctx)
	if err != nil || len(collisions) == 0 {
		return
	}
	logger := slog.SpanLogger(ctx, InstrumentationLibrary)
	for name, contributors := range collisions {
		served := make([]string, len(contributors))
		for i, typeName := range contributors {
			served[i] = namespacedToolName(typeName, name)
		}
		logger.Warn("agent tool name collision: serving the conflicting toolsets under namespaced names",
			"tool", name,
			"contributors", contributors,
			"served", served)
	}
}
