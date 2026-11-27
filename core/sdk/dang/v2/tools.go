package dangv2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/Khan/genqlient/graphql"
	telemetry "github.com/dagger/otel-go"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
	"github.com/vito/dang/v2/pkg/introspection"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// This file connects the DangCheck and DangEval builtins (see
// core/llm_dang_tools.go) to the engine: it resolves a call's scope in the
// conversation's workspace, reads the source as the workspace has it now
// (pending edits included), and runs snippets with the nested client a
// module function gets. tools_check.go and tools_eval.go do the Dang work.

func init() {
	core.RegisterDangTools(dangTools{})
}

type dangTools struct{}

func (dangTools) Check(ctx context.Context, env core.DangToolEnv, args core.DangCheckArgs) (core.DangReport, error) {
	ctx = withDangSyntaxCache(ctx)
	var rep core.DangReport
	err := withDangScope(ctx, env, args.At, args.Source, true, func(ctx context.Context, sc *dangScope, _ dangRuntime) error {
		var err error
		rep, err = checkDang(ctx, sc, args.Source, args.Search)
		rep.Text = withUnloadedNote(sc, rep.Text)
		return err
	})
	if report, ok := dangAtReport(err); ok {
		rep, err = core.DangReport{Text: report}, nil
	}
	if err != nil {
		return rep, err
	}
	// The report is the tool call's logs, as a module function's prints
	// are its call's: the engine bounds what the model sees of it.
	logDangReport(ctx, rep.Text)
	return rep, nil
}

func (dangTools) Eval(ctx context.Context, env core.DangToolEnv, args core.DangEvalArgs) (core.DangReport, error) {
	if strings.TrimSpace(args.Source) == "" {
		return core.DangReport{}, errors.New("source is required")
	}
	ctx = withDangSyntaxCache(ctx)
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return core.DangReport{}, err
	}
	var rep core.DangReport
	err = withDangScope(ctx, env, args.At, args.Source, false, func(ctx context.Context, sc *dangScope, rt dangRuntime) error {
		nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
		if err != nil {
			return err
		}
		// The nested client serves the API the snippet was checked against:
		// a module scope's module, as for its own functions, or else the
		// conversation's, installed modules and bound tool objects'
		// modules included.
		modCtx := dangModuleContext(ctx, env.Server, rt)
		switch {
		case rt.src.Self() == nil:
			nestedClientMetadata.ServedDeps = rt.deps
		case len(rt.bound) > 0:
			nestedClientMetadata.ServedDeps = core.NewSchemaBuilder(nil, rt.bound)
		}
		// The snippet acts for the agent whose turn called the tool, as a
		// module function does: its sends are attributed to that agent, and
		// a wait on it is refused rather than left to hang.
		if caller, ok := core.CallerAgent(ctx); ok {
			nestedClientMetadata.CallerAgent = caller
		}
		// A snippet is model-written code asking what things are now: the
		// image tags and git refs it looks up resolve live, as noLock: true
		// asks, so the workspace lockfile neither answers them with a pin
		// nor records one. Only the snippet's own selections are live (and
		// those of a module scope's Dang code, which runs with it): module
		// functions it calls run as clients of their own and keep the lock,
		// as does what the engine selects to serve its calls, such as a
		// module's pinned sources.
		nestedClientMetadata.LiveLookups = true
		// The report goes to the tool call's span, as a module function's
		// prints go to its call's: printed output as it is printed, the
		// rest once it is known.
		stdio := telemetry.SpanStdio(ctx, core.InstrumentationLibrary)
		defer stdio.Close()
		_, err = dangshared.WithNestedClientServer(ctx, query, nestedClientMetadata, true /* inert attachables */, nil, modCtx, func(ctx context.Context, client graphql.Client) ([]byte, error) {
			// Writes are applied while this client still serves the values
			// they name.
			write := func(ctx context.Context, writes []dangWrite) (string, error) {
				ws := make([]core.DangWrite, len(writes))
				for i, w := range writes {
					ws[i] = core.DangWrite{Name: w.name, TypeName: w.typeName, ID: w.id}
				}
				return env.Write(ctx, ws)
			}
			var err error
			rep, err = evalDang(ctx, sc, args.Source, client, stdio.Stdout, write)
			// The note follows the report, in the logs too.
			if noted := withUnloadedNote(sc, rep.Text); noted != rep.Text {
				_, _ = io.WriteString(stdio.Stdout, noted[len(rep.Text):])
				rep.Text = noted
			}
			return nil, err
		})
		return err
	})
	if report, ok := dangAtReport(err); ok {
		logDangReport(ctx, report)
		return core.DangReport{Text: report}, nil
	}
	return rep, err
}

// Reassigned names what source reassigns, by its syntax alone (see
// reassignedNames), for scheduling a DangEval call. Its scope isn't known
// here, and a module scope may be an older Dang version whose source doesn't
// parse at the current one (a `"""` with text on its opening line, or `\d` in
// it), so it takes what any of the versions modules default to finds.
func (dangTools) Reassigned(source string) []string {
	names := map[string]bool{}
	for _, v := range []dang.Version{dang.CurrentVersion, dang.Version2_4, dang.Version2_1} {
		maps.Copy(names, reassignedNames(source, v))
	}
	return slices.Sorted(maps.Keys(names))
}

// logDangReport logs a Dang tool's report to the tool call's span, as a
// module function's prints go to its call's.
func logDangReport(ctx context.Context, report string) {
	stdio := telemetry.SpanStdio(ctx, core.InstrumentationLibrary)
	defer stdio.Close()
	_, _ = io.WriteString(stdio.Stdout, report)
}

// dangRuntime is what running code in a scope needs beyond its source: the
// module source of a module scope (and the module, if the workspace loaded
// it already) and the bound tool objects' modules to serve beside it, or else
// the API to serve.
type dangRuntime struct {
	src   dagql.ObjectResult[*core.ModuleSource]
	mod   dagql.ObjectResult[*core.Module]
	bound []core.Mod
	deps  *core.SchemaBuilder
}

// dangModuleContext loads a module scope's module, best effort, so the nested
// client serves what its code calls at runtime: its dependencies and its own
// API. A module that doesn't load leaves the client the core API.
func dangModuleContext(ctx context.Context, srv *dagql.Server, rt dangRuntime) dagql.ObjectResult[*core.Module] {
	var mod dagql.ObjectResult[*core.Module]
	switch {
	case rt.mod.Self() != nil:
		return rt.mod
	case rt.src.Self() == nil:
		return mod
	}
	if err := srv.Select(ctx, rt.src, &mod, dagql.Selector{View: srv.View, Field: "asModule"}); err != nil {
		return dagql.ObjectResult[*core.Module]{}
	}
	return mod
}

// dangAtPosition matches `at` naming a position: path:line or path:line:col.
var dangAtPosition = regexp.MustCompile(`^(.+\.dang):(\d+)(?::(\d+))?$`)

// parseDangAt splits `at` into what it names and an optional position.
func parseDangAt(at string) (string, *analysis.Pos, error) {
	at = strings.TrimSpace(at)
	m := dangAtPosition.FindStringSubmatch(at)
	if m == nil {
		return at, nil, nil
	}
	line, err := strconv.Atoi(m[2])
	if err != nil || line < 1 {
		return "", nil, fmt.Errorf("at %q: invalid line", at)
	}
	col := 1
	if m[3] != "" {
		col, err = strconv.Atoi(m[3])
		if err != nil || col < 1 {
			return "", nil, fmt.Errorf("at %q: invalid column", at)
		}
	}
	return m[1], &analysis.Pos{Line: line, Column: col}, nil
}

// withDangScope resolves `at` into a scope and calls fn with it, and with
// the module source of a module scope, if the directory is one's. Only
// DangCheck takes positions. source is the call's source, if any.
func withDangScope(
	ctx context.Context,
	env core.DangToolEnv,
	at, source string,
	allowPosition bool,
	fn func(context.Context, *dangScope, dangRuntime) error,
) error {
	// Every scope binds `ws`, `chat`, `me` and the bound tool objects: the
	// default scope's API has their types, and a module's scope gets them
	// added.
	scope, defaultSchema, err := workspaceDangScope(ctx, env)
	if err != nil {
		return err
	}

	if strings.TrimSpace(at) == "" {
		sc := &dangScope{fsys: fstest.MapFS{}, dir: ".", label: "workspace scope", schema: defaultSchema, unloaded: unloadedNote(scope)}
		if err := bindDangValues(env, scope, sc, scopeNames{source: source}); err != nil {
			return err
		}
		return fn(ctx, sc, dangRuntime{deps: scope.Deps})
	}

	ws := env.Workspace
	if ws.Self() == nil {
		return fmt.Errorf("at %q: this conversation has no workspace to find it in", at)
	}
	target, pos, err := parseDangAt(at)
	if err != nil {
		return err
	}
	if pos != nil && !allowPosition {
		return fmt.Errorf("at %q: give a module or a .dang file, not a position", at)
	}
	loc, err := resolveDangLocation(ctx, env.Server, ws, target)
	if err != nil {
		return fmt.Errorf("at %q: %w", at, err)
	}
	if pos != nil && loc.file == "" {
		return fmt.Errorf("at %q: a position needs a .dang file", at)
	}
	if loc.installed != "" {
		return withInstalledModuleScope(ctx, env, scope, defaultSchema, at, source, loc, fn)
	}

	cwd := cleanWorkspacePath(strings.TrimPrefix(ws.Self().Cwd, "/"))
	label := displayWorkspacePath(cwd, loc.dir)
	var selfSource *core.ModuleSource
	if src := loc.src.Self(); src != nil {
		name := src.ModuleOriginalName
		if name == "" {
			name = src.ModuleName
		}
		if name != "" {
			label = fmt.Sprintf("module %s (%s)", name, label)
		}
		selfSource = &core.ModuleSource{ModuleName: name, ModuleOriginalName: name, EngineVersion: src.EngineVersion}
		ctx, err = ContextWithDangVersion(ctx, moduleDangVersion(src))
		if err != nil {
			return err
		}
	}

	schema := defaultSchema
	rt := dangRuntime{deps: scope.Deps}
	if loc.src.Self() != nil {
		rt = dangRuntime{src: loc.src, bound: scope.BoundMods}
		// The module the default scope loaded, if the workspace installs it.
		installed, _ := installedModule(scope, loc.name)
		rt.mod, schema, err = moduleScopeSchema(ctx, env.Server, loc.src, installed, defaultSchema, label)
		if err != nil {
			return err
		}
	}

	// A path that names nothing is the caller's mistake, as an unknown name
	// in a snippet is: say what exists instead of how the lookup failed.
	missing := func(err error) error {
		if !isNotExist(err) {
			return fmt.Errorf("at %q: %w", at, err)
		}
		if loc.file != "" {
			return &dangAtError{at: at, msg: fmt.Sprintf("no file %s in the workspace", displayWorkspacePath(cwd, loc.file))}
		}
		return &dangAtError{at: at, msg: fmt.Sprintf("%s is neither a module installed in the workspace nor one of its directories%s", target, installedNames(scope))}
	}
	var dir dagql.ObjectResult[*core.Directory]
	if err := env.Server.Select(ctx, ws, &dir, dagql.Selector{
		View:  env.Server.View,
		Field: "directory",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(workspaceAPIPath(loc.dir))}},
	}); err != nil {
		return missing(err)
	}
	mounted := false
	err = dir.Self().Mount(ctx, dir, func(mnt string) error {
		mounted = true
		if selfSource != nil {
			schema = ensureModuleSelfTypes(ctx, schema, selfSource, mnt)
		}
		sc := &dangScope{
			fsys:   subdirFS{prefix: loc.dir, fsys: os.DirFS(mnt)},
			dir:    loc.dir,
			cwd:    cwd,
			module: true,
			file:   loc.file,
			pos:    pos,
			label:  label,
			sdk:    moduleSDK(loc.src.Self()),
			schema: schema,
		}
		if loc.file != "" {
			if _, err := fs.Stat(sc.fsys, loc.file); err != nil {
				return &dangAtError{at: at, msg: fmt.Sprintf("no file %s in the workspace", sc.display(loc.file))}
			}
		}
		if err := bindDangValues(env, scope, sc, moduleScopeNames(ctx, sc.fsys, sc.dir, source)); err != nil {
			return err
		}
		return fn(ctx, sc, rt)
	})
	if err != nil && !mounted {
		return missing(err)
	}
	return err
}

// bindDangValues binds `ws`, `chat`, `me` and the bound tool objects in sc,
// but not a name its own files declare at their top level: a module's own
// `ws` (or `editor`) is the module's, as any other of its declarations would
// shadow an import, so it is not bound, and assigning it writes nothing.
// `me` is bound whenever the schema has agents: the conversation's agent, or
// null without one. `chat` is bound only where the scope's source mentions
// it: its ID holds the whole conversation, which every call would otherwise
// parse.
func bindDangValues(env core.DangToolEnv, scope *core.DangScope, sc *dangScope, names scopeNames) error {
	declared := names.declared
	var values []dangValue
	if ws := env.Workspace; ws.Self() != nil && !declared[core.DangWorkspaceBinding] {
		encoded, err := encodedID(ws.ID)
		if err != nil {
			return err
		}
		values = append(values, dangValue{name: core.DangWorkspaceBinding, typeName: "Workspace", id: encoded})
	}
	if chat := env.Chat; chat.Self() != nil && !declared[core.DangChatBinding] && names.mentions(core.DangChatBinding) && sc.schema.Types.Get("LLM") != nil {
		encoded, err := encodedID(chat.ID)
		if err != nil {
			return err
		}
		values = append(values, dangValue{name: core.DangChatBinding, typeName: "LLM", id: encoded})
	}
	if !declared[core.DangAgentBinding] && sc.schema.Types.Get("Agent") != nil {
		me := dangValue{name: core.DangAgentBinding, typeName: "Agent", readOnly: true}
		if agent := env.Agent; agent.Self() != nil {
			encoded, err := encodedID(agent.ID)
			if err != nil {
				return err
			}
			me.id = encoded
		}
		values = append(values, me)
	}
	for _, b := range scope.Bindings {
		if declared[b.Name] || sc.schema.Types.Get(b.TypeName) == nil {
			continue
		}
		values = append(values, dangValue{name: b.Name, typeName: b.TypeName, id: b.ID})
	}
	sc.values = valueDeclarations(values)
	sc.bound = values
	return nil
}

// encodedID encodes the ID id returns.
func encodedID(id func() (*call.ID, error)) (string, error) {
	i, err := id()
	if err != nil {
		return "", err
	}
	return i.Encode()
}

// scopeNames is what bindDangValues reads of a scope's source: the names its
// own files declare at their top level, and the text of its files and of the
// call's source, to tell whether a name is mentioned at all.
type scopeNames struct {
	declared map[string]bool
	source   string
	files    []string
}

// mentions reports whether the call's source or the scope's files spell
// name as a word, without resolving it: a mention in a comment or a string
// counts too, which only binds a value nothing reads.
func (n scopeNames) mentions(name string) bool {
	word := regexp.MustCompile(`(^|[^A-Za-z0-9_$])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_])`)
	if word.MatchString(n.source) {
		return true
	}
	return slices.ContainsFunc(n.files, word.MatchString)
}

// moduleScopeNames reads the names of a module scope: dir's .dang files, and
// the names they declare at their top level. Files that don't parse declare
// nothing: their diagnostics say why.
func moduleScopeNames(ctx context.Context, fsys fs.FS, dir, source string) scopeNames {
	names := scopeNames{declared: map[string]bool{}, source: source}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return names
	}
	version := dang.VersionFromContext(ctx)
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".dang" {
			continue
		}
		p := path.Join(dir, entry.Name())
		text, err := fs.ReadFile(fsys, p)
		if err != nil {
			continue
		}
		names.files = append(names.files, string(text))
		parsed, err := dang.Parse(p, text, dang.WithVersion(version))
		if err != nil {
			continue
		}
		block, ok := parsed.(*dang.FileBlock)
		if !ok {
			continue
		}
		for _, form := range block.Forms {
			for _, name := range form.DeclaredSymbols() {
				names.declared[name] = true
			}
		}
	}
	return names
}

// withMissingTypes is schema with the types of extra it lacks: a module's
// schema, with the types of the bound tool objects the default scope's API
// has. Neither schema is modified.
func withMissingTypes(schema, extra *introspection.Schema) *introspection.Schema {
	merged := *schema
	merged.Types = slices.Clone(schema.Types)
	for _, t := range extra.Types {
		if schema.Types.Get(t.Name) == nil {
			merged.Types = append(merged.Types, t)
		}
	}
	return &merged
}

// workspaceDangScope loads the default scope: the conversation's API, with
// its bound tool objects.
func workspaceDangScope(ctx context.Context, env core.DangToolEnv) (*core.DangScope, *introspection.Schema, error) {
	scope, err := env.Scope(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("load the workspace schema: %w", err)
	}
	schema, err := loadDangSchema(ctx, scope.SchemaFile)
	if err != nil {
		return nil, nil, err
	}
	return scope, schema, nil
}

// dangLocation is where `at` points in the workspace.
type dangLocation struct {
	// dir is the directory of .dang files, relative to the workspace root.
	dir string
	// file is the .dang file `at` names, if it names one.
	file string
	// src is the module whose source dir is, if there is one.
	src dagql.ObjectResult[*core.ModuleSource]

	// installed is the name of the installed module `at` names when its
	// source isn't in the workspace's tree (a git module), and source its
	// ref as the workspace installs it; dir and src are unset then.
	installed, source string

	// name is the name of the installed module `at` names when its source
	// is in the workspace's tree: dir is its root.
	name string
}

// resolveDangLocation finds what `at` (without a position) names: a module of
// the workspace by name, a directory, or a .dang file, with paths resolved as
// Workspace.directory resolves them.
func resolveDangLocation(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*core.Workspace], target string) (dangLocation, error) {
	var loc dangLocation
	switch {
	case strings.HasSuffix(target, ".dang"):
		p, err := dangWorkspacePath(ws.Self(), target)
		if err != nil {
			return loc, err
		}
		loc.file, loc.dir = p, path.Dir(p)
	default:
		source, local, ok := installedModuleSource(ctx, srv, ws, target)
		switch {
		case ok && !local:
			loc.installed, loc.source = target, source
			return loc, nil
		case ok:
			loc.dir = cleanWorkspacePath(strings.TrimPrefix(source, "/"))
			loc.name = target
		default:
			p, err := dangWorkspacePath(ws.Self(), target)
			if err != nil {
				return loc, err
			}
			loc.dir = p
		}
	}
	loc.src, loc.dir = findDangModuleSource(ctx, srv, ws, loc.dir, loc.file == "")
	return loc, nil
}

// installedModuleSource returns the source of the module installed in the
// workspace as name, and whether it is local: a directory of the workspace,
// whose root it is, rather than a ref such as a git module's.
func installedModuleSource(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*core.Workspace], name string) (source string, local, ok bool) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", false, false
	}
	var mod dagql.ObjectResult[*core.WorkspaceModule]
	if err := srv.Select(ctx, ws, &mod, dagql.Selector{
		View:  srv.View,
		Field: "module",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(name)}},
	}); err != nil {
		return "", false, false
	}
	source = mod.Self().Source
	if source == "" {
		return "", false, false
	}
	return source, workspace.IsLocalRef(source, ""), true
}

// withInstalledModuleScope is withDangScope's scope `at` an installed module
// whose source isn't in the workspace's tree, such as a git module: the
// module as the default scope loaded it, its source read-only, its API (its
// dependencies, and its own types) the schema, and the module itself what a
// snippet's client serves. A module that isn't written in Dang has no .dang
// files: DangCheck says so, and snippets still check and run against its
// API.
func withInstalledModuleScope(
	ctx context.Context,
	env core.DangToolEnv,
	scope *core.DangScope,
	defaultSchema *introspection.Schema,
	at, source string,
	loc dangLocation,
	fn func(context.Context, *dangScope, dangRuntime) error,
) error {
	mod, ok := installedModule(scope, loc.installed)
	if !ok {
		return &dangAtError{at: at, msg: unloadedModuleMessage(scope, loc)}
	}
	if !mod.Self().Source.Valid || mod.Self().Source.Value.Self() == nil {
		return &dangAtError{at: at, msg: fmt.Sprintf("module %s (%s) has no source to scope to", loc.installed, loc.source)}
	}
	src := mod.Self().Source.Value
	name := src.Self().ModuleOriginalName
	if name == "" {
		name = src.Self().ModuleName
	}
	if name == "" {
		name = loc.installed
	}
	label := fmt.Sprintf("module %s (%s)", loc.installed, loc.source)
	selfSource := &core.ModuleSource{ModuleName: name, ModuleOriginalName: name, EngineVersion: src.Self().EngineVersion}
	ctx, err := ContextWithDangVersion(ctx, moduleDangVersion(src.Self()))
	if err != nil {
		return err
	}
	_, schema, err := moduleScopeSchema(ctx, env.Server, src, mod, defaultSchema, label)
	if err != nil {
		return err
	}

	// Only the module's source directory, not the whole repository it came
	// from: its files are named by their path in that repository.
	dirPath := cleanWorkspacePath(src.Self().SourceSubpath)
	dir := src.Self().ContextDirectory
	if dir.Self() == nil {
		return &dangAtError{at: at, msg: fmt.Sprintf("module %s (%s) has no source to scope to", loc.installed, loc.source)}
	}
	if dirPath != "." {
		if err := env.Server.Select(ctx, dir, &dir, dagql.Selector{
			View:  env.Server.View,
			Field: "directory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(dirPath)}},
		}); err != nil {
			return fmt.Errorf("at %q: the source of %s: %w", at, label, err)
		}
	}
	rt := dangRuntime{src: src, mod: mod, bound: scope.BoundMods}
	return dir.Self().Mount(ctx, dir, func(mnt string) error {
		sc := &dangScope{
			fsys:   subdirFS{prefix: dirPath, fsys: os.DirFS(mnt)},
			dir:    dirPath,
			module: true,
			label:  label,
			sdk:    moduleSDK(src.Self()),
			schema: ensureModuleSelfTypes(ctx, schema, selfSource, mnt),
		}
		if err := bindDangValues(env, scope, sc, moduleScopeNames(ctx, sc.fsys, sc.dir, source)); err != nil {
			return err
		}
		return fn(ctx, sc, rt)
	})
}

// moduleScopeSchema is the schema of a module's scope, with the types of
// defaultSchema (the bound tool objects') it lacks. A Dang module's is its
// declaration-pass schema, its dependencies: its own API is what its .dang
// files declare (see ensureModuleSelfTypes). A module written with another
// SDK declares nothing Dang reads, so its scope sees its full API, as a
// snippet's client serves it: its dependencies and the module itself, mod
// if given (the module the default scope loaded) or else loaded from src.
// For such a module, it returns the module, if it loaded.
func moduleScopeSchema(
	ctx context.Context,
	srv *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
	mod dagql.ObjectResult[*core.Module],
	defaultSchema *introspection.Schema,
	label string,
) (dagql.ObjectResult[*core.Module], *introspection.Schema, error) {
	var file dagql.Result[*core.File]
	if isDangSDK(moduleSDK(src.Self())) {
		mod = dagql.ObjectResult[*core.Module]{}
	} else {
		// A module that doesn't load leaves its scope its declared schema.
		if mod.Self() == nil {
			if err := srv.Select(ctx, src, &mod, dagql.Selector{View: srv.View, Field: "asModule"}); err != nil {
				mod = dagql.ObjectResult[*core.Module]{}
			}
		}
		if mod.Self() != nil && mod.Self().Deps != nil {
			if full, err := mod.Self().Deps.Append(core.NewUserMod(mod)).SchemaIntrospectionJSONFileForModule(ctx); err == nil {
				file = full
			}
		}
	}
	if file.Self() == nil {
		var declared dagql.ObjectResult[*core.File]
		if err := srv.Select(ctx, src, &declared, dagql.Selector{View: srv.View, Field: "introspectionSchemaJson"}); err != nil {
			return mod, nil, fmt.Errorf("load the schema of %s: %w", label, err)
		}
		file = declared.Result
	}
	moduleSchema, err := loadDangSchema(ctx, file)
	if err != nil {
		return mod, nil, err
	}
	return mod, withMissingTypes(moduleSchema, defaultSchema), nil
}

// isDangSDK reports whether sdk, a module's SDK source, is Dang's, or isn't
// known.
func isDangSDK(sdk string) bool {
	sdk, _, _ = strings.Cut(sdk, "@")
	return sdk == "" || strings.Contains(strings.ToLower(sdk), "dang")
}

// installedModule is the module the default scope loaded for the one the
// workspace installs as name.
func installedModule(scope *core.DangScope, name string) (dagql.ObjectResult[*core.Module], bool) {
	if name == "" {
		return dagql.ObjectResult[*core.Module]{}, false
	}
	want := core.LatestNamer.CLIName(name)
	for _, mod := range scope.Installed {
		if core.LatestNamer.CLIName(mod.Self().Name()) == want {
			return mod, true
		}
	}
	return dagql.ObjectResult[*core.Module]{}, false
}

// unloadedModuleMessage says why an installed module isn't in the default
// scope.
func unloadedModuleMessage(scope *core.DangScope, loc dangLocation) string {
	want := core.LatestNamer.CLIName(loc.installed)
	for _, failure := range scope.LoadFailures {
		if core.LatestNamer.CLIName(failure.Name) == want {
			return fmt.Sprintf("module %s (%s) failed to load: %s", loc.installed, loc.source, failure.Message)
		}
	}
	if scope.InstalledErr != nil {
		return fmt.Sprintf("module %s (%s) is installed, but the workspace's modules didn't load: %v", loc.installed, loc.source, scope.InstalledErr)
	}
	return fmt.Sprintf("module %s (%s) is installed, but didn't load", loc.installed, loc.source)
}

// unloadedNote says which installed modules aren't in the default scope
// because they failed to load, or "" if none: a name the scope doesn't find
// may be one of them rather than a typo.
func unloadedNote(scope *core.DangScope) string {
	if scope.InstalledErr != nil {
		return fmt.Sprintf("note: the workspace's installed modules aren't in scope: %v", scope.InstalledErr)
	}
	if len(scope.LoadFailures) == 0 {
		return ""
	}
	failures := make([]string, len(scope.LoadFailures))
	for i, failure := range scope.LoadFailures {
		failures[i] = fmt.Sprintf("%s (%s)", failure.Name, firstLine(failure.Message))
	}
	slices.Sort(failures)
	return "note: installed modules that failed to load aren't in scope: " + strings.Join(failures, ", ")
}

// withUnloadedNote adds sc's note about installed modules that failed to
// load to a report that couldn't find a name.
func withUnloadedNote(sc *dangScope, out string) string {
	if sc == nil || sc.unloaded == "" || !strings.Contains(out, " not found") {
		return out
	}
	return out + sc.unloaded + "\n"
}

// installedNames lists the installed modules of the default scope, for a
// message about a name that isn't one, or "" if there are none.
func installedNames(scope *core.DangScope) string {
	var names []string
	for _, mod := range scope.Installed {
		names = append(names, mod.Self().Name())
	}
	for _, failure := range scope.LoadFailures {
		names = append(names, failure.Name+" (failed to load)")
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	return " (installed: " + strings.Join(names, ", ") + ")"
}

// moduleSDK names the SDK src is written with, or "" if none.
func moduleSDK(src *core.ModuleSource) string {
	if src == nil || src.SDK == nil {
		return ""
	}
	return src.SDK.Source
}

// isNotExist reports whether err says a path doesn't exist, as the engine's
// filesystem errors say it, whether or not they wrap fs.ErrNotExist.
func isNotExist(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "no such file or directory") || strings.Contains(msg, "does not exist")
}

// dangAtError is an `at` that names nothing to scope to. Like a type error
// in a snippet, it is reported as the call's result (see dangAtReport), not
// as a tool error.
type dangAtError struct {
	at, msg string
}

func (e *dangAtError) Error() string {
	return fmt.Sprintf("at %q: %s", e.at, e.msg)
}

// dangAtReport renders err as the call's result, if it is a dangAtError.
func dangAtReport(err error) (string, bool) {
	var atErr *dangAtError
	if !errors.As(err, &atErr) {
		return "", false
	}
	return fmt.Sprintf("error: %s\n1 error · nothing ran\n", atErr.Error()), true
}

// findDangModuleSource finds the module whose Dang source is in dir: the
// nearest module at or above it, if dir is its source directory, or, when
// rootOK, its root (dir then becomes its source directory). It returns the
// module source, if any, and the directory of .dang files.
func findDangModuleSource(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*core.Workspace], dir string, rootOK bool) (dagql.ObjectResult[*core.ModuleSource], string) {
	var none dagql.ObjectResult[*core.ModuleSource]
	// The nearest module config, of either format.
	root := ""
	for _, name := range modules.ConfigFilenames() {
		var found dagql.Nullable[dagql.String]
		if err := srv.Select(ctx, ws, &found, dagql.Selector{
			View:  srv.View,
			Field: "findUp",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString(name)},
				{Name: "from", Value: dagql.NewString(workspaceAPIPath(dir))},
			},
		}); err != nil || !found.Valid {
			continue
		}
		candidate := cleanWorkspacePath(path.Dir(strings.TrimPrefix(found.Value.String(), "/")))
		if root == "" || pathDepth(candidate) > pathDepth(root) {
			root = candidate
		}
	}
	if root == "" {
		return none, dir
	}
	var src dagql.ObjectResult[*core.ModuleSource]
	if err := srv.Select(ctx, ws, &src, dagql.Selector{
		View:  srv.View,
		Field: "moduleSource",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(workspaceAPIPath(root))}},
	}); err != nil {
		return none, dir
	}
	sub := "."
	if rel, ok := relPath(src.Self().SourceRootSubpath, src.Self().SourceSubpath); ok {
		sub = rel
	}
	srcDir := cleanWorkspacePath(path.Join(root, sub))
	switch {
	case srcDir == dir:
		return src, dir
	case rootOK && root == dir:
		return src, srcDir
	}
	return none, dir
}

// relPath is target relative to base, both slash-separated and relative to
// the same directory, when target is within base.
func relPath(base, target string) (string, bool) {
	base, target = path.Clean(base), path.Clean(target)
	switch {
	case base == target:
		return ".", true
	case base == ".":
		return target, !strings.HasPrefix(target, "../")
	case strings.HasPrefix(target, base+"/"):
		return strings.TrimPrefix(target, base+"/"), true
	}
	return "", false
}

// dangWorkspacePath resolves a path as Workspace.directory does: absolute
// from the workspace root, relative from its cwd. The result is relative to
// the root.
func dangWorkspacePath(ws *core.Workspace, p string) (string, error) {
	p = strings.ReplaceAll(p, `\`, "/")
	var resolved string
	if strings.HasPrefix(p, "/") {
		resolved = strings.TrimPrefix(path.Clean(p), "/")
	} else {
		cwd := strings.TrimPrefix(ws.Cwd, "/")
		resolved = path.Join(cwd, p)
	}
	resolved = cleanWorkspacePath(resolved)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("%s is outside the workspace", p)
	}
	return resolved, nil
}

func cleanWorkspacePath(p string) string {
	p = path.Clean(p)
	if p == "" || p == "/" {
		return "."
	}
	return p
}

// workspaceAPIPath is the absolute (root-anchored) path a Workspace API takes
// for a root-relative one.
func workspaceAPIPath(p string) string {
	if p = cleanWorkspacePath(p); p == "." {
		return "/"
	}
	return "/" + p
}

// pathDepth counts the directories of a root-relative path.
func pathDepth(p string) int {
	if p = cleanWorkspacePath(p); p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// displayWorkspacePath names a root-relative path as `at` resolves it back:
// relative to the workspace cwd when within it, else anchored at the root.
func displayWorkspacePath(cwd, p string) string {
	if rel, ok := relPath(cwd, p); ok {
		return rel
	}
	return "/" + p
}

// subdirFS serves fsys as the directory prefix of an otherwise empty file
// system, so files read from a mounted module directory are named by their
// workspace path. Nothing above prefix exists, so neither does a dang.toml
// above the module directory, which the engine never reads either (see
// dangSourceDir).
type subdirFS struct {
	prefix string
	fsys   fs.FS
}

var (
	_ fs.ReadDirFS  = subdirFS{}
	_ fs.ReadFileFS = subdirFS{}
	_ fs.StatFS     = subdirFS{}
)

func (s subdirFS) rel(op, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	switch {
	case s.prefix == "." || s.prefix == "":
		return name, nil
	case name == s.prefix:
		return ".", nil
	case strings.HasPrefix(name, s.prefix+"/"):
		return strings.TrimPrefix(name, s.prefix+"/"), nil
	}
	return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
}

func (s subdirFS) Open(name string) (fs.File, error) {
	rel, err := s.rel("open", name)
	if err != nil {
		return nil, err
	}
	return s.fsys.Open(rel)
}

func (s subdirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	rel, err := s.rel("readdir", name)
	if err != nil {
		return nil, err
	}
	return fs.ReadDir(s.fsys, rel)
}

func (s subdirFS) ReadFile(name string) ([]byte, error) {
	rel, err := s.rel("readfile", name)
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(s.fsys, rel)
}

func (s subdirFS) Stat(name string) (fs.FileInfo, error) {
	rel, err := s.rel("stat", name)
	if err != nil {
		return nil, err
	}
	return fs.Stat(s.fsys, rel)
}
