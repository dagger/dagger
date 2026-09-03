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

const syntheticSDKScopesGenerator = "scopes"

func (s *workspaceSchema) syntheticSDKGenerators(
	ctx context.Context,
	staged *stagedWorkspaceConfig,
	include []string,
) ([]*core.Generator, error) {
	names := make([]string, 0, len(staged.Config.SDKs))
	for name := range staged.Config.SDKs {
		names = append(names, name)
	}
	sort.Strings(names)

	var generators []*core.Generator
	for _, sdkName := range names {
		selected, err := selectSDKModule(staged.Config, sdkName)
		if err != nil {
			return nil, err
		}

		providerName := selected.entry.Module
		leafName := "generate"
		root := &core.ModTreeNode{Parent: &core.ModTreeNode{}, Name: providerName}
		node := &core.ModTreeNode{
			Parent:      root,
			Name:        leafName,
			Description: "Generate SDK-managed scopes",
			IsGenerator: true,
		}
		generator := &core.Generator{
			Node: node,
			Synthetic: &core.SyntheticGeneratorSpec{
				Name:        providerName + ":" + leafName,
				Path:        []string{providerName, leafName},
				Description: node.Description,
				Provider:    sdkName,
				Kind:        syntheticSDKScopesGenerator,
			},
		}
		filtered, err := filterGeneratorsByInclude(ctx, []*core.Generator{generator}, include, false)
		if err != nil {
			return nil, err
		}
		generators = append(generators, filtered...)
	}
	return generators, nil
}

// runSyntheticSDKGenerator executes one persisted engine-owned SDK generator.
// GeneratorGroup binds the Workspace that produced the generator into ctx.
func runSyntheticSDKGenerator(
	ctx context.Context,
	spec *core.SyntheticGeneratorSpec,
) (dagql.ObjectResult[*core.Changeset], error) {
	base, err := syntheticGeneratorWorkspace(ctx, dagql.ObjectResult[*core.Workspace]{})
	if err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}
	return runSDKModuleGeneratorGraph(ctx, base, []*core.SyntheticGeneratorSpec{spec})
}

func syntheticGeneratorWorkspace(
	ctx context.Context,
	bound dagql.ObjectResult[*core.Workspace],
) (dagql.ObjectResult[*core.Workspace], error) {
	if bound.Self() != nil {
		return bound, nil
	}
	if base, ok := core.WorkspaceFromContext(ctx); ok {
		return base, nil
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	var base dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, dag.Root(), &base, dagql.Selector{Field: "currentWorkspace"}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("load synthetic generator workspace: %w", err)
	}
	return base, nil
}

type sdkModuleGraphScope struct {
	key         string
	sdkName     string
	configScope string
	path        string
	scope       workspace.SDKScope
}

func sdkModuleScopeKey(sdkName, scopePath string) string {
	return sdkName + ":" + cleanWorkspaceRelPath(scopePath)
}

func validateSDKModuleGenerationCycles(cfg *workspace.Config, configDir string) error {
	if cfg == nil {
		return nil
	}
	type cycleNode struct {
		key     string
		path    string
		clients []string
	}
	var nodes []*cycleNode
	moduleByPath := map[string]*cycleNode{}
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
				return fmt.Errorf("SDK %q scope: %w", sdkName, err)
			}
			node := &cycleNode{
				key:     sdkModuleScopeKey(sdkName, workspaceScope),
				path:    cleanWorkspaceRelPath(workspaceScope),
				clients: entry.Scopes[configScope].Clients,
			}
			nodes = append(nodes, node)
			if entry.Scopes[configScope].IsModule {
				moduleByPath[node.path] = node
			}
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].path != nodes[j].path {
			return nodes[i].path < nodes[j].path
		}
		return nodes[i].key < nodes[j].key
	})

	dependencies := func(node *cycleNode) ([]*cycleNode, error) {
		seen := map[string]bool{}
		var deps []*cycleNode
		for _, target := range node.clients {
			if !workspace.IsLocalRef(target, "") {
				continue
			}
			resolved, err := resolveSDKManagedClientModule(configDir, target)
			if err != nil {
				return nil, fmt.Errorf("resolve client target %q in scope %q: %w", target, node.path, err)
			}
			dep := moduleByPath[cleanWorkspaceRelPath(resolved)]
			if dep == nil || seen[dep.key] {
				continue
			}
			seen[dep.key] = true
			deps = append(deps, dep)
		}
		sort.Slice(deps, func(i, j int) bool { return deps[i].path < deps[j].path })
		return deps, nil
	}

	state := map[string]uint8{}
	var stack []*cycleNode
	var visit func(*cycleNode) error
	visit = func(node *cycleNode) error {
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
		deps, err := dependencies(node)
		if err != nil {
			return err
		}
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[node.key] = 2
		return nil
	}
	for _, node := range nodes {
		if err := visit(node); err != nil {
			return err
		}
	}
	return nil
}

// runSDKModuleGeneratorGraph runs selected synthetic generators as one
// threaded workspace graph. A scope that generates a client for a local module
// depends on that target module's generation. The graph is validated before an
// SDK call, then folded in stable leaf-first order.
type sdkModuleGeneratorPlan struct {
	invocationCWD string
	ordered       []*sdkModuleGraphScope
}

func selectSDKModuleGeneratorProviders(specs []*core.SyntheticGeneratorSpec) (map[string]bool, error) {
	selected := map[string]bool{}
	for _, spec := range specs {
		if spec == nil {
			continue
		}
		if spec.Kind != syntheticSDKScopesGenerator {
			return nil, fmt.Errorf("unknown synthetic SDK generator kind %q", spec.Kind)
		}
		selected[spec.Provider] = true
	}
	return selected, nil
}

func loadSDKModuleGraphScopes(
	cfg *workspace.Config,
	configDir string,
) ([]*sdkModuleGraphScope, map[string]*sdkModuleGraphScope, error) {
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
				return nil, nil, fmt.Errorf("SDK %q scope: %w", sdkName, err)
			}
			workspaceScope = cleanWorkspaceRelPath(workspaceScope)
			node := &sdkModuleGraphScope{
				key:         sdkModuleScopeKey(sdkName, workspaceScope),
				sdkName:     sdkName,
				configScope: configScope,
				path:        workspaceScope,
				scope:       entry.Scopes[configScope],
			}
			if node.scope.IsModule && strings.TrimSpace(node.scope.Name) == "" {
				return nil, nil, fmt.Errorf("SDK %q module scope %q has no name; run `dagger module init --name=NAME --path=%s --sdk=%s`", sdkName, workspaceScope, workspaceScope, sdkName)
			}
			scopes = append(scopes, node)
			if !node.scope.IsModule {
				continue
			}
			if existing := moduleByPath[node.path]; existing != nil && existing.sdkName != node.sdkName {
				return nil, nil, fmt.Errorf(
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
	return scopes, moduleByPath, nil
}

func planSDKModuleGeneratorGraph(
	base dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	specs []*core.SyntheticGeneratorSpec,
) (*sdkModuleGeneratorPlan, error) {
	selectedProviders, err := selectSDKModuleGeneratorProviders(specs)
	if err != nil {
		return nil, err
	}
	return planSDKModuleScopes(
		cleanWorkspaceRelPath(base.Self().Cwd),
		staged.Config,
		staged.ConfigDir,
		selectedProviders,
	)
}

func planSDKModuleScopes(
	invocationCWD string,
	cfg *workspace.Config,
	configDir string,
	selectedProviders map[string]bool,
) (*sdkModuleGeneratorPlan, error) {
	scopes, moduleByPath, err := loadSDKModuleGraphScopes(cfg, configDir)
	if err != nil {
		return nil, err
	}

	plan := &sdkModuleGeneratorPlan{
		invocationCWD: cleanWorkspaceRelPath(invocationCWD),
	}

	dependencies := func(node *sdkModuleGraphScope) ([]*sdkModuleGraphScope, error) {
		seen := map[string]bool{}
		var deps []*sdkModuleGraphScope
		for _, target := range node.scope.Clients {
			if !workspace.IsLocalRef(target, "") {
				continue
			}
			resolved, err := resolveSDKManagedClientModule(configDir, target)
			if err != nil {
				return nil, fmt.Errorf("resolve client target %q in scope %q: %w", target, node.path, err)
			}
			dep := moduleByPath[cleanWorkspaceRelPath(resolved)]
			if dep == nil || seen[dep.key] {
				continue
			}
			seen[dep.key] = true
			deps = append(deps, dep)
		}
		sort.Slice(deps, func(i, j int) bool {
			if deps[i].path != deps[j].path {
				return deps[i].path < deps[j].path
			}
			return deps[i].sdkName < deps[j].sdkName
		})
		return deps, nil
	}

	state := map[string]uint8{}
	var stack []*sdkModuleGraphScope
	var visit func(*sdkModuleGraphScope) error
	visit = func(node *sdkModuleGraphScope) error {
		switch state[node.key] {
		case 2:
			return nil
		case 1:
			cycle := []string{}
			start := 0
			for i, item := range stack {
				if item.key == node.key {
					start = i
					break
				}
			}
			for _, item := range stack[start:] {
				cycle = append(cycle, item.path)
			}
			cycle = append(cycle, node.path)
			return fmt.Errorf("local SDK generation cycle: %s", strings.Join(cycle, " -> "))
		}
		state[node.key] = 1
		stack = append(stack, node)
		deps, err := dependencies(node)
		if err != nil {
			return err
		}
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[node.key] = 2
		plan.ordered = append(plan.ordered, node)
		return nil
	}
	for _, node := range scopes {
		if !selectedProviders[node.sdkName] || !sdkGenerationScopeApplies(plan.invocationCWD, node.path) {
			continue
		}
		if !node.scope.IsModule && len(node.scope.Clients) == 0 {
			continue
		}
		if err := visit(node); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func runSDKModuleGeneratorGraph(
	ctx context.Context,
	base dagql.ObjectResult[*core.Workspace],
	specs []*core.SyntheticGeneratorSpec,
) (dagql.ObjectResult[*core.Changeset], error) {
	s := &workspaceSchema{}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, base.Self(), workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}
	if err := validateSDKModuleGenerationCycles(staged.Config, staged.ConfigDir); err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}

	plan, err := planSDKModuleGeneratorGraph(base, staged, specs)
	if err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}
	current := base
	for _, node := range plan.ordered {
		selected, err := selectSDKModule(staged.Config, node.sdkName)
		if err != nil {
			return dagql.ObjectResult[*core.Changeset]{}, err
		}
		settings, err := effectiveSDKModuleSettings(ctx, current.Self(), staged.Config, node.sdkName, node.configScope)
		if err != nil {
			return dagql.ObjectResult[*core.Changeset]{}, err
		}
		provider, err := s.loadWorkspaceSDKModule(ctx, current.Self(), staged.ConfigDir, selected.ref, settings)
		if err != nil {
			return dagql.ObjectResult[*core.Changeset]{}, err
		}
		scoped, err := workspaceAtSDKModuleScope(ctx, current, node.path)
		if err != nil {
			return dagql.ObjectResult[*core.Changeset]{}, err
		}

		operationCtx, clients, err := s.resolveSDKModuleScopeClients(ctx, current, staged, node.scope.Clients)
		if err != nil {
			return dagql.ObjectResult[*core.Changeset]{}, fmt.Errorf("resolve clients in scope %q: %w", node.path, err)
		}
		current, err = provider.GenerateScope(operationCtx, scoped, node.scope.IsModule, node.scope.Name, clients)
		if err == nil {
			current, err = s.validateSDKModuleWorkspace(operationCtx, scoped, current, node.path, staged.ConfigFile)
		}
		if err == nil && node.scope.IsModule {
			err = s.validateGeneratedModuleConfig(operationCtx, current, node.path)
		}
		if err != nil {
			return dagql.ObjectResult[*core.Changeset]{}, fmt.Errorf("generate SDK scope %q: %w", node.path, err)
		}
	}

	changes, err := s.workspaceChangesBetween(ctx, base, current)
	if err != nil {
		return dagql.ObjectResult[*core.Changeset]{}, err
	}
	return reRootChangesetToCwd(ctx, changes, plan.invocationCWD)
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
	selectedWorkspace, overlayLock, err := s.prepareWorkspaceOverlayLock(operationCtx, current.Self(), staged.ConfigDir)
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
		target, err := s.resolveClientTargetModule(operationCtx, selectedWorkspace, moduleLoadRef)
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

func sdkModuleClientTargetInputRef(cfg *workspace.Config, configDir, cwd, target string) (string, error) {
	if entry, ok := cfg.Modules[target]; ok {
		return workspace.ResolveModuleEntrySource(configDir, entry.Source), nil
	}
	if !workspace.IsLocalRef(target, "") {
		return target, nil
	}
	resolved, err := resolveWorkspacePath(target, cwd)
	if err != nil {
		return "", fmt.Errorf("module target %q must not escape the workspace root", target)
	}
	return resolved, nil
}
