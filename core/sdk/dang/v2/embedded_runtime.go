package dangv2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/core"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
	"github.com/vito/dang/v2/pkg/ioctx"
	"go.opentelemetry.io/otel/trace"
)

// moduleRuntimeFunctionName is the method an embedded runtime file must
// declare on exactly one of its types: pub moduleRuntime(modSource:
// ModuleSource!, introspectionJson: File): Container!. The introspectionJson
// argument is part of the contract shape but never passed: embedded runtimes
// exist only with dagger-module.toml configs, which never regenerate bindings
// at runtime.
const moduleRuntimeFunctionName = "moduleRuntime"

// moduleRuntimeSignature is the locked argument and return shape of
// moduleRuntime, reported verbatim when a runtime file deviates from it.
const moduleRuntimeSignature = "moduleRuntime(modSource: ModuleSource!, introspectionJson: File): Container!"

// EmbeddedRuntimeContainer evaluates a module's embedded runtime file with
// the native Dang interpreter and returns the runtime container its
// moduleRuntime function produces. contents is the Dang source of the file
// named filename, read from the module's source root directory.
//
// scopedMod is the partially initialized module being loaded, serving as the
// nested client's module context — that makes the nested client inherit the
// caller's workspace binding instead of attempting host workspace detection,
// which a nested client (having no session attachables of its own) cannot do.
// Its deps schema is what the nested client is served, so the same deps
// introspection is what the file is inferred against.
func (Impl) EmbeddedRuntimeContainer(
	ctx context.Context,
	query *core.Query,
	deps *core.SchemaBuilder,
	source dagql.ObjectResult[*core.ModuleSource],
	scopedMod dagql.ObjectResult[*core.Module],
	filename string,
	contents string,
) (inst dagql.ObjectResult[*core.Container], rerr error) {
	defer func() {
		if rerr != nil {
			rerr = dangshared.ConvertError(rerr)
		}
	}()

	clientMetadata, nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return inst, err
	}

	schemaFile, err := deps.SchemaIntrospectionJSONFileForModule(ctx)
	if err != nil {
		return inst, fmt.Errorf("schema introspection for embedded runtime: %w", err)
	}

	sourceID, err := source.ID()
	if err != nil {
		return inst, fmt.Errorf("embedded runtime module source ID: %w", err)
	}
	encodedSourceID, err := sourceID.Encode()
	if err != nil {
		return inst, fmt.Errorf("encode embedded runtime module source ID: %w", err)
	}

	ctrIDBytes, err := dangshared.WithNestedClientServer(ctx, query, nestedClientMetadata, clientMetadata.ClientID, true, nil, scopedMod, func(ctx context.Context, gqlClient graphql.Client) ([]byte, error) {
		var intro introspection.Response
		f, err := schemaFile.Self().Open(ctx, dagql.ObjectResult[*core.File]{Result: schemaFile})
		if err != nil {
			return nil, fmt.Errorf("open schema file: %w", err)
		}
		defer f.Close()
		if err := json.NewDecoder(f).Decode(&intro); err != nil {
			return nil, fmt.Errorf("decode schema: %w", err)
		}

		ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
			Name:       "Dagger",
			Client:     gqlClient,
			Schema:     intro.Schema,
			AutoImport: true,
		})

		// Route the file's stdout/stderr to the user-facing span; see the
		// matching setup in evalDangSource for why the current span won't do.
		stdioCtx := trace.ContextWithSpanContext(ctx, dagql.UserFacingSpanContext(ctx))
		stdio := telemetry.SpanStdio(stdioCtx, core.InstrumentationLibrary)
		ctx = ioctx.StdoutToContext(ctx, stdio.Stdout)
		ctx = ioctx.StderrToContext(ctx, stdio.Stderr)

		env, err := runEmbeddedRuntimeFile(ctx, filename, contents)
		if err != nil {
			return nil, fmt.Errorf("evaluate embedded runtime %q: %w", filename, err)
		}

		ctor, fnType, err := findEmbeddedRuntimeConstructor(env, filename)
		if err != nil {
			return nil, err
		}

		self, err := ctor.Call(ctx, env, map[string]dang.Value{})
		if err != nil {
			return nil, fmt.Errorf("construct embedded runtime type: %w", err)
		}

		modSourceArg, err := embeddedRuntimeModSourceArg(ctx, ctor, fnType, encodedSourceID)
		if err != nil {
			return nil, err
		}

		call := &dang.FunCall{
			Fun: &dang.Select{
				Receiver: &dang.ValueNode{Val: self},
				Field:    &dang.Symbol{Name: moduleRuntimeFunctionName},
			},
			Args: dang.Record{{
				Key:   "modSource",
				Value: &dang.ValueNode{Val: modSourceArg},
			}},
		}
		result, err := call.Eval(ctx, ctor.Closure)
		if err != nil {
			return nil, fmt.Errorf("call embedded runtime %s: %w", moduleRuntimeFunctionName, err)
		}

		ctrVal, ok := result.(dang.GraphQLValue)
		if !ok {
			return nil, fmt.Errorf("embedded runtime %s returned %T, expected a Container", moduleRuntimeFunctionName, result)
		}
		ctrID, err := ctrVal.ID(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve embedded runtime container: %w", err)
		}
		return []byte(ctrID), nil
	})
	if err != nil {
		return inst, err
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("current dagql server for embedded runtime: %w", err)
	}
	var ctrID dagql.ID[*core.Container]
	if err := ctrID.Decode(string(ctrIDBytes)); err != nil {
		return inst, fmt.Errorf("embedded runtime %s must return a Container: %w", moduleRuntimeFunctionName, err)
	}
	inst, err = ctrID.Load(ctx, dag)
	if err != nil {
		return inst, fmt.Errorf("load embedded runtime container: %w", err)
	}
	return inst, nil
}

// runEmbeddedRuntimeFile evaluates the embedded runtime source as a
// single-file Dang module and returns its value scope. The file is staged
// alone in a temporary directory so evaluation sees exactly the named file —
// never sibling .dang files from the module's own source tree — while reusing
// the same directory evaluation path as regular Dang modules.
func runEmbeddedRuntimeFile(ctx context.Context, filename string, contents string) (dang.ValueScope, error) {
	tmpDir, err := os.MkdirTemp("", "dagger-embedded-runtime-")
	if err != nil {
		return nil, fmt.Errorf("stage embedded runtime: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, filename), []byte(contents), 0o600); err != nil {
		return nil, fmt.Errorf("stage embedded runtime: %w", err)
	}
	return dang.RunDir(ctx, tmpDir, false)
}

// findEmbeddedRuntimeConstructor returns the constructor of the single type
// in the evaluated file that declares a pub moduleRuntime function, along
// with that function's type.
func findEmbeddedRuntimeConstructor(env dang.ValueScope, filename string) (*dang.ConstructorFunction, *hm.FunctionType, error) {
	var (
		names   []string
		ctor    *dang.ConstructorFunction
		fnType  *hm.FunctionType
		binding = env.Bindings(dang.PublicVisibility)
	)
	for _, bind := range binding {
		typeCtor, ok := bind.Value.(*dang.ConstructorFunction)
		if !ok {
			continue
		}
		methodType, ok := pubMethodType(typeCtor.ObjectType, moduleRuntimeFunctionName)
		if !ok {
			continue
		}
		names = append(names, bind.Key)
		ctor = typeCtor
		fnType = methodType
	}
	switch len(names) {
	case 0:
		return nil, nil, fmt.Errorf("embedded runtime %q must declare a type with a pub %s function", filename, moduleRuntimeFunctionName)
	case 1:
		if err := validateEmbeddedRuntimeSignature(filename, names[0], ctor, fnType); err != nil {
			return nil, nil, err
		}
		return ctor, fnType, nil
	default:
		return nil, nil, fmt.Errorf("embedded runtime %q declares %s on multiple types: %s", filename, moduleRuntimeFunctionName, strings.Join(names, ", "))
	}
}

// validateEmbeddedRuntimeSignature enforces the locked cross-repo shape of
// the embedded runtime contract, so a mismatch fails with a precise error
// here instead of a confusing type error mid-call.
func validateEmbeddedRuntimeSignature(filename, typeName string, ctor *dang.ConstructorFunction, fnType *hm.FunctionType) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("embedded runtime %q: %s; expected %s", filename, fmt.Sprintf(format, args...), moduleRuntimeSignature)
	}

	if !ctor.IsAutoCallable() {
		return fail("type %s must be constructible without arguments", typeName)
	}

	if fnType.Block() != nil {
		return fail("%s must not declare a block parameter", moduleRuntimeFunctionName)
	}

	args, ok := fnType.Arg().(*dang.RecordType)
	if !ok {
		return fail("%s arguments have type %T, expected a record", moduleRuntimeFunctionName, fnType.Arg())
	}

	var seenModSource, seenIntrospectionJSON bool
	for _, arg := range args.Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return fail("%s argument %s is not a monotype", moduleRuntimeFunctionName, arg.Key)
		}
		switch arg.Key {
		case "modSource":
			if !isNonNullNamed(argType, "ModuleSource") {
				return fail("%s argument modSource has type %s, expected ModuleSource!", moduleRuntimeFunctionName, argType)
			}
			seenModSource = true
		case "introspectionJson":
			// The engine never passes introspectionJson, so a non-null
			// declaration could never be satisfied.
			if !isNamed(argType, "File") {
				return fail("%s argument introspectionJson has type %s, expected the optional type File", moduleRuntimeFunctionName, argType)
			}
			seenIntrospectionJSON = true
		default:
			return fail("%s declares unexpected argument %s", moduleRuntimeFunctionName, arg.Key)
		}
	}
	if !seenModSource {
		return fail("%s must declare a modSource argument", moduleRuntimeFunctionName)
	}
	if !seenIntrospectionJSON {
		return fail("%s must declare an introspectionJson argument", moduleRuntimeFunctionName)
	}

	if ret := fnType.Ret(false); !isNonNullNamed(ret, "Container") {
		return fail("%s returns %s, expected Container!", moduleRuntimeFunctionName, ret)
	}
	return nil
}

func isNonNullNamed(t hm.Type, name string) bool {
	nonNull, ok := t.(hm.NonNullType)
	if !ok {
		return false
	}
	return isNamed(nonNull.Type, name)
}

func isNamed(t hm.Type, name string) bool {
	named, ok := t.(*dang.Type)
	return ok && named.Named == name
}

func pubMethodType(objType *dang.Type, name string) (*hm.FunctionType, bool) {
	for fieldName, scheme := range objType.Bindings(dang.PublicVisibility) {
		if fieldName != name {
			continue
		}
		t, mono := scheme.Type()
		if !mono {
			return nil, false
		}
		fnType, ok := t.(*hm.FunctionType)
		return fnType, ok
	}
	return nil, false
}

// embeddedRuntimeModSourceArg converts the module source's encoded ID into
// the Dang value expected by moduleRuntime's modSource argument, reusing the
// same ID-to-value conversion as regular function call dispatch.
func embeddedRuntimeModSourceArg(ctx context.Context, ctor *dang.ConstructorFunction, fnType *hm.FunctionType, encodedSourceID string) (dang.Value, error) {
	args, ok := fnType.Arg().(*dang.RecordType)
	if !ok {
		return nil, fmt.Errorf("embedded runtime %s arguments have type %T, expected a record", moduleRuntimeFunctionName, fnType.Arg())
	}
	for _, arg := range args.Fields {
		if arg.Key != "modSource" {
			continue
		}
		argType, mono := arg.Value.Type()
		if !mono {
			return nil, fmt.Errorf("embedded runtime %s modSource argument is not a monotype", moduleRuntimeFunctionName)
		}
		conv := dangConverter{env: ctor.Closure}
		val, err := conv.convert(ctx, encodedSourceID, argType)
		if err != nil {
			return nil, fmt.Errorf("convert embedded runtime modSource argument: %w", err)
		}
		return val, nil
	}
	return nil, fmt.Errorf("embedded runtime %s must declare a modSource argument", moduleRuntimeFunctionName)
}
