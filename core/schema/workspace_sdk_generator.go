package schema

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type sdkModuleGraphScope struct {
	key          string
	sdkName      string
	configScope  string
	path         string
	scope        workspace.SDKScope
	dependencies []*sdkModuleGraphScope
}

func sdkModuleScopeKey(sdkName, scopePath string) string {
	return sdkName + ":" + cleanWorkspaceRelPath(scopePath)
}

func validateSDKModuleGenerationGraph(cfg *workspace.Config, configDir string) error {
	if cfg == nil {
		return nil
	}
	scopes, err := loadSDKModuleGraphScopes(cfg, configDir)
	if err != nil {
		return err
	}
	_, err = orderSDKModuleGraph(scopes)
	return err
}

// runSDKModuleGeneratorGraph runs SDK scopes as one
// threaded workspace graph. A scope that generates a client for a local module
// depends on that target module's generation. The graph is validated before an
// SDK call, then folded in stable leaf-first order.
type sdkModuleGeneratorPlan struct {
	invocationCWD string
	ordered       []*sdkModuleGraphScope
}

func loadSDKModuleGraphScopes(
	cfg *workspace.Config,
	configDir string,
) ([]*sdkModuleGraphScope, error) {
	var scopes []*sdkModuleGraphScope
	moduleByPath := map[string]*sdkModuleGraphScope{}
	sdkNames := make([]string, 0, len(cfg.SDKs))
	for sdkName := range cfg.SDKs {
		sdkNames = append(sdkNames, sdkName)
	}
	sort.Strings(sdkNames)
	for _, sdkName := range sdkNames {
		entry := cfg.SDKs[sdkName]
		configScopes := make([]string, 0, len(entry.Scopes))
		for configScope := range entry.Scopes {
			configScopes = append(configScopes, configScope)
		}
		sort.Strings(configScopes)
		for _, configScope := range configScopes {
			workspaceScope, err := workspace.ResolveSDKManagedPath(configDir, configScope)
			if err != nil {
				return nil, fmt.Errorf("SDK %q scope: %w", sdkName, err)
			}
			workspaceScope = cleanWorkspaceRelPath(workspaceScope)
			node := &sdkModuleGraphScope{
				key:         sdkModuleScopeKey(sdkName, workspaceScope),
				sdkName:     sdkName,
				configScope: configScope,
				path:        workspaceScope,
				scope:       entry.Scopes[configScope],
			}
			scopes = append(scopes, node)
			if !node.scope.IsModule {
				continue
			}
			if existing := moduleByPath[node.path]; existing != nil && existing.sdkName != node.sdkName {
				return nil, fmt.Errorf(
					"module scope %q is managed by SDKs %q and %q",
					node.path,
					existing.sdkName,
					node.sdkName,
				)
			}
			moduleByPath[node.path] = node
		}
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].path != scopes[j].path {
			return scopes[i].path < scopes[j].path
		}
		return scopes[i].sdkName < scopes[j].sdkName
	})
	for _, scope := range scopes {
		dependencies, err := sdkModuleGraphDependencies(scope, moduleByPath, configDir)
		if err != nil {
			return nil, err
		}
		scope.dependencies = dependencies
	}
	return scopes, nil
}

func sdkModuleGraphDependencies(
	node *sdkModuleGraphScope,
	moduleByPath map[string]*sdkModuleGraphScope,
	configDir string,
) ([]*sdkModuleGraphScope, error) {
	seen := map[string]bool{}
	var dependencies []*sdkModuleGraphScope
	for _, target := range node.scope.Clients {
		resolved, err := resolveSDKManagedClientModule(configDir, target)
		if err != nil {
			return nil, fmt.Errorf("resolve client target %q in scope %q: %w", target, node.path, err)
		}
		if !workspace.IsLocalRef(resolved, "") {
			continue
		}
		dependency := moduleByPath[cleanWorkspaceRelPath(resolved)]
		if dependency == nil || seen[dependency.key] {
			continue
		}
		// A scope may record a client for its own module — `dagger module client
		// add .` — so a module can call itself through a generated client. That
		// is a target like any other for the SDK, not a generation dependency:
		// the scope's own pass renders it, so no edge, and no false self-cycle.
		if dependency.key == node.key {
			continue
		}
		seen[dependency.key] = true
		dependencies = append(dependencies, dependency)
	}
	sort.Slice(dependencies, func(i, j int) bool {
		if dependencies[i].path != dependencies[j].path {
			return dependencies[i].path < dependencies[j].path
		}
		return dependencies[i].sdkName < dependencies[j].sdkName
	})
	return dependencies, nil
}

func orderSDKModuleGraph(
	scopes []*sdkModuleGraphScope,
) ([]*sdkModuleGraphScope, error) {
	state := map[string]uint8{}
	var stack []*sdkModuleGraphScope
	var ordered []*sdkModuleGraphScope
	var visit func(*sdkModuleGraphScope) error
	visit = func(node *sdkModuleGraphScope) error {
		switch state[node.key] {
		case 2:
			return nil
		case 1:
			start := 0
			for i, item := range stack {
				if item.key == node.key {
					start = i
					break
				}
			}
			cycle := make([]string, 0, len(stack)-start+1)
			for _, item := range stack[start:] {
				cycle = append(cycle, item.path)
			}
			cycle = append(cycle, node.path)
			return fmt.Errorf("local SDK generation cycle: %s", strings.Join(cycle, " -> "))
		}
		state[node.key] = 1
		stack = append(stack, node)
		for _, dependency := range node.dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[node.key] = 2
		ordered = append(ordered, node)
		return nil
	}
	for _, node := range scopes {
		if err := visit(node); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func planSDKModuleScopes(
	invocationCWD string,
	cfg *workspace.Config,
	configDir string,
	selectedProviders map[string]bool,
) (*sdkModuleGeneratorPlan, error) {
	scopes, err := loadSDKModuleGraphScopes(cfg, configDir)
	if err != nil {
		return nil, err
	}
	ordered, err := orderSDKModuleGraph(scopes)
	if err != nil {
		return nil, err
	}

	plan := &sdkModuleGeneratorPlan{
		invocationCWD: cleanWorkspaceRelPath(invocationCWD),
	}
	required := map[string]bool{}
	var requireScope func(*sdkModuleGraphScope)
	requireScope = func(node *sdkModuleGraphScope) {
		if required[node.key] {
			return
		}
		required[node.key] = true
		for _, dependency := range node.dependencies {
			requireScope(dependency)
		}
	}
	for _, node := range scopes {
		if !selectedProviders[node.sdkName] || !sdkGenerationScopeApplies(plan.invocationCWD, node.path) {
			continue
		}
		if !node.scope.IsModule && len(node.scope.Clients) == 0 {
			continue
		}
		requireScope(node)
	}
	for _, node := range ordered {
		if required[node.key] {
			plan.ordered = append(plan.ordered, node)
		}
	}
	return plan, nil
}

func runSDKModuleGeneratorGraph(
	ctx context.Context,
	base dagql.ObjectResult[*core.Workspace],
	sdkName string,
) (_ dagql.ObjectResult[*core.Workspace], rerr error) {
	s := &workspaceSchema{}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, base.Self(), workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	plan, err := planSDKModuleScopes(cleanWorkspaceRelPath(base.Self().Cwd), staged.Config, staged.ConfigDir, map[string]bool{sdkName: true})
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	current := base
	output := base
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return output, err
	}
	for _, node := range plan.ordered {
		selected, err := selectSDKModule(staged.Config, node.sdkName)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}

		before := current
		current, err = s.generateSDKModuleScope(ctx, current, staged, selected, node.configScope, node.path, node.scope)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("generate SDK scope %q: %w", node.path, err)
		}
		// Dependencies prepare client inputs. Only this provider's changes are
		// returned, so selecting several providers does not repeat their changes.
		if node.sdkName == sdkName {
			changes, err := s.workspaceChangesBetween(ctx, before, current)
			if err != nil {
				return output, err
			}
			id, err := changes.ID()
			if err != nil {
				return output, err
			}
			if err := dag.Select(ctx, output, &output, dagql.Selector{
				Field: "withChanges", Args: []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](id)}},
			}); err != nil {
				return output, err
			}
		}
	}

	return output, nil
}

func (s *workspaceSchema) resolveSDKModuleScopeClients(
	ctx context.Context,
	current dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	targets []string,
) (context.Context, []dagql.ObjectResult[*core.ModuleSource], error) {
	operationCtx := ctx
	var err error
	if current.Self().ClientID != "" {
		operationCtx, err = s.withWorkspaceClientContext(ctx, current.Self())
		if err != nil {
			return ctx, nil, err
		}
	}
	_, overlayLock, err := s.prepareWorkspaceOverlayLock(operationCtx, current.Self(), staged.ConfigDir)
	if err != nil {
		return operationCtx, nil, err
	}
	operationCtx = withWorkspaceLookupLockOverride(operationCtx, overlayLock.Lock)

	clients := make([]dagql.ObjectResult[*core.ModuleSource], 0, len(targets))
	for _, recordedTarget := range targets {
		moduleLoadRef, err := resolveSDKManagedClientModule(staged.ConfigDir, recordedTarget)
		if err != nil {
			return operationCtx, nil, err
		}
		target, err := s.resolveClientTargetModule(operationCtx, current, moduleLoadRef)
		if err != nil {
			return operationCtx, nil, err
		}
		clients = append(clients, target)
	}
	return operationCtx, clients, nil
}

func sdkGenerationScopeApplies(invocationCWD, scope string) bool {
	return workspacePathContains(invocationCWD, scope) || workspacePathContains(scope, invocationCWD)
}
