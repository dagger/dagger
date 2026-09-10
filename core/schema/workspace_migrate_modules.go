package schema

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/sdk/sdkmeta"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceMigrateArgs struct {
	Modules []string `default:"[]"`
}

type workspaceMigrateModuleArgs struct {
	Path string `default:"."`
}

// migrate plans all required conversions before exposing a changeset. Optional
// module paths are explicit inputs so API callers use the same plan as the CLI.
func (s *workspaceSchema) migrate(ctx context.Context, ws *core.Workspace, args workspaceMigrateArgs) (*core.WorkspaceMigration, error) {
	stage, err := s.stageWorkspaceMigration(ctx, ws)
	if err != nil {
		return nil, err
	}
	if stage.configPath == "" {
		return stage.legacy, nil
	}
	planner, err := s.newModuleMigrationPlanner(ctx, ws, stage.staged, stage.configPath)
	if err != nil {
		return nil, err
	}
	if err := planner.requiredModules(stage.convertedModules); err != nil {
		return nil, err
	}
	for _, selected := range args.Modules {
		dir, err := migrationModulePath(ws.Cwd, selected)
		if err != nil {
			return nil, err
		}
		if err := planner.module(dir, true, false); err != nil {
			return nil, err
		}
	}
	migration, err := planner.result(ctx, stage.base, stage.staged)
	if err != nil {
		return nil, err
	}
	migration.ModuleCandidates = planner.optionalCandidates()
	migration.Steps = append(stage.legacy.Steps, migration.Steps...)
	return migration, nil
}

type workspaceMigrationStage struct {
	base, staged     dagql.ObjectResult[*core.Directory]
	configPath       string
	convertedModules []string
	legacy           *core.WorkspaceMigration
}

// stageWorkspaceMigration reuses the legacy conversion and resolves its SDK
// references before the installed-module phase. It never exports host files.
func (s *workspaceSchema) stageWorkspaceMigration(ctx context.Context, ws *core.Workspace) (*workspaceMigrationStage, error) {
	legacy, err := s.migrateLegacy(ctx, ws)
	if err != nil {
		return nil, err
	}
	stage := &workspaceMigrationStage{legacy: legacy, configPath: ws.ConfigFile}
	empty, err := legacy.Changes.IsEmpty(ctx)
	if err != nil {
		return nil, err
	}
	if empty {
		if stage.configPath == "" {
			for _, step := range legacy.Steps {
				if len(step.Warnings) > 0 {
					return nil, fmt.Errorf("workspace migration is incomplete; legacy configuration was left unchanged: %s", strings.Join(step.Warnings, "; "))
				}
			}
			return stage, nil
		}
		stage.base, err = s.resolveRootfs(ctx, ws, ".", core.CopyFilter{}, false)
		if err != nil {
			return nil, err
		}
		stage.staged = stage.base
		return stage, nil
	}

	stage.base, stage.staged = legacy.Changes.Before, legacy.Changes.After
	paths, err := legacy.Changes.ComputePaths(ctx)
	if err != nil {
		return nil, err
	}
	for _, file := range paths.Added {
		if path.Base(file) == workspace.ModuleConfigFileName {
			stage.convertedModules = append(stage.convertedModules, path.Dir(file))
		}
	}
	var configs []string
	for _, file := range append(paths.Added, paths.Modified...) {
		if path.Base(file) == workspace.ConfigFileName {
			configs = append(configs, file)
		}
	}
	if err := stage.ensureConfig(ctx, ws.Cwd, configs); err != nil {
		return nil, err
	}
	if err := stage.resolveSDKs(ctx, configs); err != nil {
		return nil, err
	}
	return stage, nil
}

func (stage *workspaceMigrationStage) ensureConfig(ctx context.Context, cwd string, configs []string) error {
	if stage.configPath != "" {
		return nil
	}
	if len(configs) > 0 {
		var err error
		stage.configPath, err = owningMigrationConfig(configs, cwd)
		return err
	}
	remaining, err := workspaceMigrationPathExists(ctx, stage.staged, workspace.LegacyModuleConfigFileName)
	if err != nil {
		return err
	}
	if remaining {
		return fmt.Errorf("root dagger.json has not been migrated; migrate the workspace root first")
	}
	stage.configPath = workspace.ConfigFileName
	stage.staged, err = withWorkspaceMigrationFile(ctx, stage.staged, stage.configPath, nil, "workspace configuration")
	return err
}

func (stage *workspaceMigrationStage) resolveSDKs(ctx context.Context, configs []string) error {
	updates, err := migratedConfigSDKUpdates(configs, func(file string) ([]byte, error) {
		return core.DirectoryReadFile(ctx, stage.staged, file)
	})
	if err != nil {
		return err
	}
	for _, file := range configs {
		if data, ok := updates[file]; ok {
			stage.staged, err = withWorkspaceMigrationFile(ctx, stage.staged, file, data, "workspace SDK configuration")
			if err != nil {
				return err
			}
		}
	}
	// SDK reference corrections remain in the reviewed legacy workspace step.
	changes, err := workspaceMigrationChanges(ctx, stage.staged, stage.base)
	if err != nil {
		return err
	}
	stage.legacy.Changes = changes.Self()
	for _, step := range stage.legacy.Steps {
		step.Changes = changes.Self()
	}
	return nil
}

// migrateModule converts only the selected module. Without a native workspace,
// it creates no workspace config and does not migrate dependencies or fixtures.
func (s *workspaceSchema) migrateModule(ctx context.Context, ws *core.Workspace, args workspaceMigrateModuleArgs) (*core.WorkspaceMigration, error) {
	if ws.HostPath() == "" {
		return nil, fmt.Errorf("module migration is local-only")
	}
	base, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{}, false)
	if err != nil {
		return nil, err
	}
	planner, err := s.newModuleMigrationPlanner(ctx, ws, base, ws.ConfigFile)
	if err != nil {
		return nil, err
	}
	dir, err := migrationModulePath(ws.Cwd, args.Path)
	if err != nil {
		return nil, err
	}
	if err := planner.module(dir, true, false); err != nil {
		return nil, err
	}
	return planner.result(ctx, base, base)
}

type moduleMigrationPlanner struct {
	configPath    string
	configData    []byte
	config        *workspace.Config
	files         map[string]bool
	visited       map[string]bool
	active        map[string]bool
	warnings      []string
	readFile      func(string) ([]byte, error)
	writes        map[string][]byte
	removed       map[string]bool
	cleanupModule func(string, *modules.ModuleConfig) error
	cleanups      []workspaceMigrationGitignoreCleanup
}

func (s *workspaceSchema) newModuleMigrationPlanner(ctx context.Context, ws *core.Workspace, staged dagql.ObjectResult[*core.Directory], configPath string) (*moduleMigrationPlanner, error) {
	planner := &moduleMigrationPlanner{
		configPath: configPath,
		files:      map[string]bool{}, visited: map[string]bool{}, active: map[string]bool{},
		writes: map[string][]byte{}, removed: map[string]bool{},
		readFile: func(file string) ([]byte, error) { return core.DirectoryReadFile(ctx, staged, file) },
	}
	if configPath != "" {
		data, err := planner.readFile(configPath)
		if err != nil {
			return nil, err
		}
		cfg, err := workspace.ParseConfig(data)
		if err != nil {
			return nil, err
		}
		planner.configData, planner.config = data, cfg
		applyMigratedSDKFixups(cfg, planMigratedSDKFixups(cfg))
	}
	for _, name := range []string{workspace.ConfigFileName, workspace.ModuleConfigFileName, workspace.LegacyModuleConfigFileName} {
		for _, pattern := range []string{name, "**/" + name} {
			files, err := staged.Self().Glob(ctx, staged, pattern)
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				if strings.Contains("/"+file+"/", "/.git/") {
					continue
				}
				planner.files[path.Clean(file)] = true
			}
		}
	}
	planner.cleanupModule = func(dir string, cfg *modules.ModuleConfig) error {
		if cfg.SDK == nil {
			return nil
		}
		workspaceCtx, err := s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return err
		}
		srv, err := core.CurrentDagqlServer(workspaceCtx)
		if err != nil {
			return err
		}
		projectRoot := filepath.Join(ws.HostPath(), filepath.FromSlash(dir))
		cleanup, err := s.workspaceMigrationGitignoreCleanup(workspaceCtx, srv, ws, staged, &workspace.CompatWorkspace{
			Config: cfg, ProjectRoot: projectRoot,
			ConfigPath: filepath.Join(projectRoot, workspace.LegacyModuleConfigFileName),
		})
		if err != nil {
			return err
		}
		if cleanup != nil {
			planner.cleanups = append(planner.cleanups, *cleanup)
		}
		return nil
	}
	return planner, nil
}

func (p *moduleMigrationPlanner) requiredModules(convertedModules []string) error {
	for _, dir := range convertedModules {
		if err := p.module(dir, false, true); err != nil {
			return fmt.Errorf("required migrated module %s: %w", dir, err)
		}
	}
	if p.config == nil {
		return nil
	}
	var installed []string
	for name, entry := range p.config.Modules {
		if workspace.IsLocalRef(entry.Source, entry.Pin) {
			installed = append(installed, name)
		}
	}
	sort.Strings(installed)
	for _, name := range installed {
		source := p.config.Modules[name].Source
		dir, err := migrationLocalRefPath(path.Dir(p.configPath), source)
		if err != nil {
			return fmt.Errorf("required installed module %q (%s): %w", name, source, err)
		}
		if err := p.module(dir, false, true); err != nil {
			return fmt.Errorf("required installed module %q (%s): %w", name, source, err)
		}
	}
	return nil
}

func (p *moduleMigrationPlanner) result(ctx context.Context, base, staged dagql.ObjectResult[*core.Directory]) (*core.WorkspaceMigration, error) {
	moduleBase := staged
	if p.config != nil {
		applyMigratedSDKFixups(p.config, planMigratedSDKFixups(p.config))
		updated, err := migrationConfigBytes(p.configData, p.config)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(updated, p.configData) {
			p.writes[p.configPath] = updated
		}
	}
	var paths []string
	for file := range p.writes {
		paths = append(paths, file)
	}
	sort.Strings(paths)
	var err error
	for _, file := range paths {
		staged, err = withWorkspaceMigrationFile(ctx, staged, file, p.writes[file], "module migration configuration")
		if err != nil {
			return nil, err
		}
	}
	paths = paths[:0]
	for file := range p.removed {
		paths = append(paths, file)
	}
	sort.Strings(paths)
	for _, file := range paths {
		staged, err = removeWorkspaceMigrationFile(ctx, staged, file, "remove legacy module configuration", "remove legacy module configuration")
		if err != nil {
			return nil, err
		}
	}
	staged, err = applyWorkspaceMigrationGitignoreCleanups(ctx, staged, p.cleanups)
	if err != nil {
		return nil, err
	}
	changes, err := workspaceMigrationChanges(ctx, staged, base)
	if err != nil {
		return nil, err
	}
	migration := &core.WorkspaceMigration{Changes: changes.Self(), ConfigFile: p.configPath}
	moduleChanges, err := workspaceMigrationChanges(ctx, staged, moduleBase)
	if err != nil {
		return nil, err
	}
	empty, err := moduleChanges.Self().IsEmpty(ctx)
	if err != nil {
		return nil, err
	}
	if !empty || len(p.warnings) > 0 {
		migration.Steps = []*core.WorkspaceMigrationStep{{
			Code: "local-modules", Description: "Migrated local modules",
			Warnings: p.warnings, Changes: moduleChanges.Self(),
		}}
	}
	return migration, nil
}

func migrationRelativeCwd(cwd string) (string, error) {
	raw := strings.TrimLeft(filepath.ToSlash(cwd), "/")
	cleaned := path.Clean(raw)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("workspace cwd %q escapes workspace root", cwd)
	}
	return cleaned, nil
}

func migrationConfigBytes(original []byte, cfg *workspace.Config) ([]byte, error) {
	before, err := workspace.ParseConfig(original)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(workspace.SerializeConfig(before), workspace.SerializeConfig(cfg)) {
		return original, nil
	}
	return workspace.UpdateConfigBytes(original, cfg)
}

func owningMigrationConfig(configs []string, cwd string) (string, error) {
	cwd, err := migrationRelativeCwd(cwd)
	if err != nil {
		return "", err
	}
	cwd = path.Clean(filepath.ToSlash(cwd))
	var owner string
	for _, file := range configs {
		file = path.Clean(filepath.ToSlash(file))
		dir := path.Dir(file)
		if dir != "." && cwd != dir && !strings.HasPrefix(cwd, dir+"/") {
			continue
		}
		if owner == "" || len(file) > len(owner) {
			owner = file
		}
	}
	if owner == "" {
		return "", fmt.Errorf("migration did not create a workspace config that owns %s", cwd)
	}
	return owner, nil
}

func migratedConfigSDKUpdates(paths []string, readFile func(string) ([]byte, error)) (map[string][]byte, error) {
	updates := make(map[string][]byte, len(paths))
	for _, file := range paths {
		data, err := readFile(file)
		if err != nil {
			return nil, fmt.Errorf("read migrated config %s: %w", file, err)
		}
		cfg, err := workspace.ParseConfig(data)
		if err != nil {
			return nil, fmt.Errorf("parse migrated config %s: %w", file, err)
		}
		fixes := planMigratedSDKFixups(cfg)
		if len(fixes) == 0 {
			continue
		}
		applyMigratedSDKFixups(cfg, fixes)
		updates[file], err = workspace.UpdateConfigBytes(data, cfg)
		if err != nil {
			return nil, err
		}
	}
	return updates, nil
}

func migrationModulePath(cwd, target string) (string, error) {
	var err error
	cwd, err = migrationRelativeCwd(cwd)
	if err != nil {
		return "", err
	}
	target = filepath.ToSlash(target)
	if path.IsAbs(target) {
		target = strings.TrimPrefix(target, "/")
	} else {
		target = path.Join(cwd, target)
	}
	target = path.Clean(target)
	if target == ".." || strings.HasPrefix(target, "../") {
		return "", fmt.Errorf("module path escapes the workspace: %s", target)
	}
	return target, nil
}

func migrationLocalRefPath(dir, source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("local module source is empty")
	}
	if path.IsAbs(filepath.ToSlash(source)) {
		return "", fmt.Errorf("local module %q: absolute source is outside migration scope", source)
	}
	return migrationModulePath(dir, source)
}

func (p *moduleMigrationPlanner) inScope(dir string) bool {
	if path.IsAbs(dir) || dir == ".." || strings.HasPrefix(dir, "../") {
		return false
	}
	configDir := path.Dir(p.configPath)
	if configDir != "." && dir != configDir && !strings.HasPrefix(dir, configDir+"/") {
		return false
	}
	for current := dir; current != configDir; current = path.Dir(current) {
		if p.files[path.Join(current, workspace.ConfigFileName)] || current == "." {
			return false
		}
	}
	return true
}

func (p *moduleMigrationPlanner) module(dir string, explicit, followDependencies bool) error {
	legacyPath := path.Join(dir, workspace.LegacyModuleConfigFileName)
	nativePath := path.Join(dir, workspace.ModuleConfigFileName)
	legacy := p.files[legacyPath]
	if !p.inScope(dir) {
		if !explicit && !legacy && p.files[nativePath] {
			// A native module owned by another native workspace needs no
			// conversion here. Its owner manages its SDK and dependencies.
			return nil
		}
		return fmt.Errorf("module %s belongs to another workspace; migrate its owning workspace first", dir)
	}
	if p.active[dir] {
		p.warnings = append(p.warnings, fmt.Sprintf("Local module dependency cycle includes %s; review the dependency graph before generation.", dir))
		return nil
	}
	if p.visited[dir] {
		return nil
	}
	p.visited[dir] = true
	p.active[dir] = true
	defer delete(p.active, dir)
	if !legacy && !p.files[nativePath] {
		return fmt.Errorf("no module configuration found at %s", dir)
	}
	if legacy && p.files[nativePath] {
		return fmt.Errorf("both %s and %s exist; keep the intended configuration before migration", legacyPath, nativePath)
	}
	readPath := legacyPath
	if !legacy {
		readPath = nativePath
	}
	data, err := p.readFile(readPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", readPath, err)
	}
	var cfg *modules.ModuleConfig
	if legacy {
		cfg, err = workspace.ParseLegacyModuleConfigTolerant(data)
	} else {
		var parsed *modules.ModuleConfigWithUserFields
		parsed, err = modules.ParseModuleConfigForFilename(data, nativePath)
		if err == nil {
			cfg = &parsed.ModuleConfig
		}
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", readPath, err)
	}
	var plan *workspace.ModuleMigrationPlan
	if legacy {
		plan, err = workspace.PlanModuleMigration(cfg, false)
		if err != nil {
			return fmt.Errorf("migrate %s: %w", legacyPath, err)
		}
	}
	if followDependencies {
		for _, dep := range workspace.LocalModuleRefs(cfg) {
			target, err := migrationLocalRefPath(dir, dep.Source)
			if err != nil {
				return fmt.Errorf("required dependency %q (%s) of module %s: %w", dep.Name, dep.Source, dir, err)
			}
			if err := p.module(target, false, true); err != nil {
				return fmt.Errorf("required dependency %q (%s) of module %s: %w", dep.Name, dep.Source, dir, err)
			}
		}
	}
	if err := p.registerSDK(dir, cfg); err != nil {
		return err
	}
	if plan != nil {
		if p.cleanupModule != nil {
			if err := p.cleanupModule(dir, cfg); err != nil {
				return fmt.Errorf("clean legacy .gitignore for module %s: %w", dir, err)
			}
		}
		p.writes[nativePath] = plan.ConfigData
		p.removed[legacyPath] = true
		delete(p.files, legacyPath)
		p.files[nativePath] = true
	}
	return nil
}

func (p *moduleMigrationPlanner) registerSDK(dir string, cfg *modules.ModuleConfig) error {
	if cfg.SDK == nil || p.config == nil {
		return nil
	}
	modulePath, err := filepath.Rel(filepath.FromSlash(path.Dir(p.configPath)), filepath.FromSlash(dir))
	if err != nil {
		return err
	}
	modulePath = filepath.ToSlash(modulePath)
	sdkSource := workspace.MigratedModuleSDKSource(cfg, modulePath)
	base, version, _ := strings.Cut(sdkSource, "@")
	if !strings.ContainsAny(base, "/\\") {
		if resolved, _, _, err := sdkmeta.ResolveInstall(base); err == nil {
			sdkSource = resolved
			if version != "" {
				sdkSource += "@" + version
			}
		}
	}
	if err := workspace.RegisterMigratedModuleSDK(p.config, sdkSource, modulePath, cfg.Name, workspace.MigratedModuleClients(cfg, modulePath)...); err != nil {
		return fmt.Errorf("register SDK for %s: %w", dir, err)
	}
	return nil
}

func (p *moduleMigrationPlanner) optionalCandidates() []string {
	var candidates []string
	for file := range p.files {
		if path.Base(file) != workspace.LegacyModuleConfigFileName {
			continue
		}
		dir := path.Dir(file)
		if p.visited[dir] || p.files[path.Join(dir, workspace.ModuleConfigFileName)] {
			continue
		}
		if !p.inScope(dir) {
			continue
		}
		candidates = append(candidates, dir)
	}
	sort.Strings(candidates)
	return candidates
}
