package schema

import (
	"context"
	"errors"
	"fmt"
	"runtime"
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

// runSDKModuleGeneratorGraph runs SDK scopes as one workspace graph. A scope
// that generates a client for a local module depends on that target module's
// generation. The graph is validated before an SDK call. Scopes generate as
// soon as their dependencies have, and their changes fold in stable leaf-first
// order.
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
) (dagql.ObjectResult[*core.Workspace], error) {
	s := &workspaceSchema{}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, base.Self(), workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	plan, err := planSDKModuleScopes(cleanWorkspaceRelPath(base.Self().Cwd), staged.Config, staged.ConfigDir, map[string]bool{sdkName: true})
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	// A scope reads its own files and the modules it generates clients for, so
	// it generates from the workspace with only its local dependencies'
	// changes applied, and scopes that do not depend on each other generate at
	// once, up to one per CPU: a scope's generation builds and runs code, and
	// a workspace can have hundreds of scopes.
	slots := make(chan struct{}, runtime.NumCPU())
	generations := make(map[string]*sdkModuleScopeGeneration, len(plan.ordered))
	for _, node := range plan.ordered {
		generations[node.key] = &sdkModuleScopeGeneration{done: make(chan struct{})}
	}
	upstream := sdkModuleGraphUpstream(plan.ordered)
	for _, node := range plan.ordered {
		generation := generations[node.key]
		go func() {
			defer close(generation.done)
			input := base
			for _, dependency := range plan.ordered {
				if !upstream[node.key][dependency.key] {
					continue
				}
				prior := generations[dependency.key]
				<-prior.done
				if prior.err != nil || prior.skipped {
					generation.skipped = true
					return
				}
				if prior.changes.Self() == nil {
					continue
				}
				if input, generation.err = workspaceWithChanges(ctx, dag, input, prior.changes); generation.err != nil {
					return
				}
			}
			slots <- struct{}{}
			defer func() { <-slots }()
			generation.changes, generation.err = s.generateSDKModuleGraphScope(ctx, input, staged, node)
		}()
	}

	// Changes apply in plan order, so a dependency's changes come before those
	// of the scopes that read them. Only this provider's changes are returned:
	// dependencies of another provider only prepare client inputs, and
	// selecting several providers does not repeat their changes.
	var errs []error
	output := base
	for _, node := range plan.ordered {
		generation := generations[node.key]
		<-generation.done
		if generation.err != nil {
			errs = append(errs, fmt.Errorf("generate SDK scope %q: %w", node.path, generation.err))
		}
		if len(errs) > 0 || generation.skipped || node.sdkName != sdkName || generation.changes.Self() == nil {
			continue
		}
		if output, err = workspaceWithChanges(ctx, dag, output, generation.changes); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return dagql.ObjectResult[*core.Workspace]{}, errors.Join(errs...)
	}
	return output, nil
}

// sdkModuleScopeGeneration is one scope's generation in a graph run. done
// closes once the scope has generated, failed, or been skipped because a
// dependency failed. changes holds nothing when the scope changed nothing.
type sdkModuleScopeGeneration struct {
	done    chan struct{}
	changes dagql.ObjectResult[*core.Changeset]
	err     error
	skipped bool
}

// sdkModuleGraphUpstream maps each scope to every scope it depends on,
// directly or through another. ordered must list dependencies first.
func sdkModuleGraphUpstream(ordered []*sdkModuleGraphScope) map[string]map[string]bool {
	upstream := make(map[string]map[string]bool, len(ordered))
	for _, node := range ordered {
		keys := map[string]bool{}
		for _, dependency := range node.dependencies {
			keys[dependency.key] = true
			for key := range upstream[dependency.key] {
				keys[key] = true
			}
		}
		upstream[node.key] = keys
	}
	return upstream
}

// generateSDKModuleGraphScope generates one scope of a graph run and returns
// what it changed in input, or nothing when it changed nothing.
func (s *workspaceSchema) generateSDKModuleGraphScope(
	ctx context.Context,
	input dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	node *sdkModuleGraphScope,
) (dagql.ObjectResult[*core.Changeset], error) {
	var none dagql.ObjectResult[*core.Changeset]
	selected, err := selectSDKModule(staged.Config, node.sdkName)
	if err != nil {
		return none, err
	}
	generated, err := s.generateSDKModuleScope(ctx, input, staged, selected, node.configScope, node.path, node.scope)
	if err != nil {
		return none, err
	}
	changes, err := s.workspaceChangesBetween(ctx, input, generated)
	if err != nil {
		return none, err
	}
	empty, err := changes.Self().IsEmpty(ctx)
	if err != nil || empty {
		return none, err
	}
	return changes, nil
}

func workspaceWithChanges(
	ctx context.Context,
	dag *dagql.Server,
	ws dagql.ObjectResult[*core.Workspace],
	changes dagql.ObjectResult[*core.Changeset],
) (dagql.ObjectResult[*core.Workspace], error) {
	id, err := changes.ID()
	if err != nil {
		return ws, err
	}
	var updated dagql.ObjectResult[*core.Workspace]
	err = dag.Select(ctx, ws, &updated, dagql.Selector{
		Field: "withChanges", Args: []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](id)}},
	})
	return updated, err
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
