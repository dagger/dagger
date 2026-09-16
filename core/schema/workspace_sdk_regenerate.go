package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
)

// regenerateSDKModuleClients adds a single progress group for generation caused
// by a workspace mutation. Only the already selected scopes are generated.
func (s *workspaceSchema) regenerateSDKModuleClients(ctx context.Context, updated dagql.ObjectResult[*core.Workspace], staged *stagedWorkspaceConfig, selections []sdkModuleClientSelection) (_ dagql.ObjectResult[*core.Workspace], rerr error) {
	if len(selections) == 0 {
		return updated, nil
	}
	ctx, span := core.Tracer(ctx).Start(ctx, "re-generate", telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)
	plan, err := planSDKModuleClientRegeneration(staged, selections)
	if err != nil {
		return updated, err
	}
	err = runSDKModuleClientRegeneration(ctx, plan, func(ctx context.Context, node *sdkModuleGraphScope) error {
		selected, err := selectSDKModule(staged.Config, node.sdkName)
		if err != nil {
			return err
		}
		updated, err = s.generateSDKModuleScope(ctx, updated, staged, selected, node.configScope, node.path, node.scope)
		return err
	})
	return updated, err
}

func planSDKModuleClientRegeneration(staged *stagedWorkspaceConfig, selections []sdkModuleClientSelection) (*sdkModuleGeneratorPlan, error) {
	ordered, err := orderSDKModuleClientSelections(staged, selections)
	if err != nil {
		return nil, err
	}
	plan := &sdkModuleGeneratorPlan{}
	modules := map[string]*sdkModuleGraphScope{}
	targets := map[string][]string{}
	for _, selection := range ordered {
		node := &sdkModuleGraphScope{
			key:         sdkModuleScopeKey(selection.sdkName, selection.workspaceScope),
			sdkName:     selection.sdkName,
			configScope: selection.configScopePath,
			path:        selection.workspaceScope,
			scope:       selection.scope,
		}
		plan.ordered = append(plan.ordered, node)
		targets[node.key] = selection.targets
		if node.scope.IsModule {
			modules[node.path] = node
		}
	}
	for _, node := range plan.ordered {
		dependencies, err := sdkModuleGraphDependencies(node, modules, staged.ConfigDir)
		if err != nil {
			return nil, err
		}
		for _, dep := range dependencies {
			// A single selected scope previously bypassed graph validation. A self
			// reference does not represent another generation operation to display.
			if dep != node {
				node.dependencies = append(node.dependencies, dep)
			}
		}
	}
	if err := plan.planProgress(staged.ConfigDir, targets); err != nil {
		return nil, err
	}
	return plan, nil
}

func runSDKModuleClientRegeneration(ctx context.Context, plan *sdkModuleGeneratorPlan, generate func(context.Context, *sdkModuleGraphScope) error) (rerr error) {
	if len(plan.ordered) == 0 {
		return nil
	}
	progress := &sdkModuleGeneratorProgress{plan: plan, scopes: map[string]*sdkModuleScopeProgress{}}
	defer func() { progress.finish(len(plan.ordered)-1, rerr) }()
	for i, node := range plan.ordered {
		scopeCtx := progress.start(ctx, node)
		err := generate(scopeCtx, node)
		progress.finish(i, err)
		if err != nil {
			return fmt.Errorf("re-generate scope %q: %w", node.path, err)
		}
	}
	return nil
}
