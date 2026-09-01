package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
)

// embeddedRuntimePrefix marks a runtime source of the form embed:<filename>:
// the module's runtime is a generated Dang file committed next to its
// dagger-module.toml, evaluated in-process by the engine's native Dang
// interpreter. The form is produced only by SDK codegen (via targetRuntime)
// and consumed only here — it is not a user-facing ref format.
const embeddedRuntimePrefix = "embed:"

// IsEmbeddedRuntimeSource reports whether source uses the internal
// embed:<filename> runtime scheme.
func IsEmbeddedRuntimeSource(source string) bool {
	return strings.HasPrefix(source, embeddedRuntimePrefix)
}

// EmbeddedRuntimeFilename extracts and validates the filename payload of an
// embed:<filename> runtime source. The file is read from the module's own
// source root directory (the directory containing its dagger-module.toml),
// so the payload must be a bare .dang filename: path separators and dot
// directories are rejected rather than resolved.
func EmbeddedRuntimeFilename(source string) (string, error) {
	filename := strings.TrimPrefix(source, embeddedRuntimePrefix)
	switch {
	case filename == "":
		return "", fmt.Errorf("embedded runtime ref %q: missing filename", source)
	case filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`):
		return "", fmt.Errorf("embedded runtime ref %q: filename must be a bare file name", source)
	// The filename is spliced into include-pattern lists when the module
	// context is loaded, where these are pattern metacharacters. SDK codegen
	// only ever emits a plain runtime.dang, so rejecting them here cannot
	// break a producer.
	case strings.ContainsAny(filename, `*?[]{}!`):
		return "", fmt.Errorf("embedded runtime ref %q: filename must not contain pattern metacharacters", source)
	case !strings.HasSuffix(filename, ".dang"):
		return "", fmt.Errorf("embedded runtime ref %q: filename must end in .dang", source)
	}
	return filename, nil
}

// embeddedRuntimeSDK is the SDK behind embed:<filename> runtime sources. It
// loads no SDK module at all: the named file is read from the module's own
// source root and evaluated with the native Dang interpreter to produce the
// runtime container. It implements only Runtime — like a runtime-only SDK
// module, the module's typedefs are then discovered through the runtime
// container itself.
type embeddedRuntimeSDK struct {
	root     *core.Query
	filename string
}

var _ core.SDK = (*embeddedRuntimeSDK)(nil)

func (sdk *embeddedRuntimeSDK) CloneForModuleSource(*core.ModuleSource) core.SDK {
	if sdk == nil {
		return nil
	}
	cp := *sdk
	return &cp
}

func (sdk *embeddedRuntimeSDK) AsRuntime() (core.Runtime, bool) {
	return sdk, true
}

func (sdk *embeddedRuntimeSDK) AsModuleTypes() (core.ModuleTypes, bool) {
	return nil, false
}

func (sdk *embeddedRuntimeSDK) AsCodeGenerator() (core.CodeGenerator, bool) {
	return nil, false
}

func (sdk *embeddedRuntimeSDK) AsClientGenerator() (core.ClientGenerator, bool) {
	return nil, false
}

func (sdk *embeddedRuntimeSDK) AsModuleInitializer() (core.ModuleInitializer, bool) {
	return nil, false
}

func (sdk *embeddedRuntimeSDK) AsClientInitializer() (core.ClientInitializer, bool) {
	return nil, false
}

func (sdk *embeddedRuntimeSDK) AsRuntimeTarget() (core.RuntimeTarget, bool) {
	return nil, false
}

func (sdk *embeddedRuntimeSDK) AsModule() (dagql.ObjectResult[*core.Module], bool) {
	return dagql.ObjectResult[*core.Module]{}, false
}

func (sdk *embeddedRuntimeSDK) AttachDependencyResults(
	context.Context,
	func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (sdk *embeddedRuntimeSDK) Runtime(
	ctx context.Context,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
) (_ core.ModuleRuntime, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "embedded runtime: load "+sdk.filename)
	defer telemetry.EndWithCause(span, &rerr)

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for embedded runtime: %w", err)
	}

	source, err = scopeSourceForSDKOperation(ctx, source, "runtime", dag)
	if err != nil {
		return nil, fmt.Errorf("failed to scope module source for embedded runtime: %w", err)
	}

	contents, err := sdk.readRuntimeFile(ctx, dag, source)
	if err != nil {
		return nil, err
	}

	// The nested client evaluating the runtime file needs a module context so
	// it inherits the caller's workspace binding (see EmbeddedRuntimeContainer).
	// The module is only partially initialized at runtime-load time, so build
	// the same minimal scoped module the Dang SDK uses for its typedef pass.
	src := source.Self()
	scopedMod, err := ScopeModuleForSDKOperation(ctx, &core.Module{
		Source:        dagql.NonNull(source),
		ContextSource: dagql.NonNull(source),
		NameField:     src.ModuleName,
		OriginalName:  src.ModuleOriginalName,
		SDKConfig:     src.SDK,
		Deps:          deps,
	}, "embeddedRuntime", dag)
	if err != nil {
		return nil, fmt.Errorf("failed to scope module for embedded runtime: %w", err)
	}

	// Route through the same Dang major ladder as the Dang SDK, so copying a
	// major carries the capability forward automatically; majors predating
	// embedded runtimes simply don't implement it.
	impl, ok := dangImplFor(source.Self()).(embeddedRuntimeEvaluator)
	if !ok {
		return nil, fmt.Errorf("embedded runtime %q requires a newer module engine version (module targets %s)", sdk.filename, source.Self().EngineVersion)
	}
	ctr, err := impl.EmbeddedRuntimeContainer(ctx, sdk.root, deps, source, scopedMod, sdk.filename, contents)
	if err != nil {
		return nil, err
	}

	var inst dagql.ObjectResult[*core.Container]
	if err := dag.Select(ctx, ctr, &inst,
		dagql.Selector{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(RuntimeWorkdirPath)},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("failed to set embedded runtime workdir: %w", err)
	}
	return &core.ContainerRuntime{Container: inst}, nil
}

// readRuntimeFile reads the embedded runtime file from the module's source
// root directory — the directory containing its dagger-module.toml.
func (sdk *embeddedRuntimeSDK) readRuntimeFile(
	ctx context.Context,
	dag *dagql.Server,
	source dagql.ObjectResult[*core.ModuleSource],
) (string, error) {
	src := source.Self()
	var contents string
	err := dag.Select(ctx, src.ContextDirectory, &contents,
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(filepath.Join(src.SourceRootSubpath, sdk.filename))},
			},
		},
		dagql.Selector{Field: "contents"},
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf(
				"module %q: embedded runtime file %q not found next to %s; run `dagger generate` and commit the generated files",
				src.ModuleName, sdk.filename, src.ConfigFilename,
			)
		}
		return "", fmt.Errorf("failed to read embedded runtime file %q: %w", sdk.filename, err)
	}
	return contents, nil
}
