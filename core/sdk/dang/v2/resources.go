package dangv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/introspection"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// EntrypointResourcesMember is the optional entrypoint member that returns
// include patterns for files outside the module directory.
const EntrypointResourcesMember = "resources"

// ErrEntrypointResourcesCallsAPI is the error of every API request that the
// program makes while the engine evaluates resources.
var ErrEntrypointResourcesCallsAPI = errors.New("entrypoint resources must not call the Dagger API")

// refusingClient fails every request before anything is sent. The engine
// caches resources on the entrypoint program alone, which is correct only if
// the evaluation cannot read anything else.
type refusingClient struct{}

func (refusingClient) MakeRequest(context.Context, *graphql.Request, *graphql.Response) error {
	return ErrEntrypointResourcesCallsAPI
}

// MentionsEntrypointResources reports whether a top-level Dang file of dir
// contains the member name. It is the cheap first half of the declaration
// gate; DeclaresEntrypointResources is the precise second half.
func MentionsEntrypointResources(dir string) (bool, error) {
	files, err := entrypointFiles(dir)
	if err != nil {
		return false, err
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("read module entrypoint file %q: %w", filepath.Base(path), err)
		}
		if bytes.Contains(data, []byte(EntrypointResourcesMember)) {
			return true, nil
		}
	}
	return false, nil
}

// DeclaresEntrypointResources reports whether a type that implements
// ModuleEntrypoint in the top-level Dang files of dir declares a public
// resources member, and refuses a declaration the engine cannot call. A
// program that does not parse declares nothing, so its error surfaces where it
// does without the member: when the module loads its types.
func DeclaresEntrypointResources(dir string) (bool, error) {
	files, err := entrypointFiles(dir)
	if err != nil {
		return false, err
	}
	var decls []*dang.FieldDecl
	for _, path := range files {
		root, err := dang.ParseFile(path)
		if err != nil {
			return false, nil //nolint:nilerr
		}
		if file, ok := root.(*dang.FileBlock); ok {
			decls = append(decls, entrypointResourcesDecls(file)...)
		}
	}
	for _, decl := range decls {
		if err := checkEntrypointResourcesDecl(decl); err != nil {
			return true, err
		}
	}
	return len(decls) > 0, nil
}

// entrypointFiles lists the files that runEntrypointDir runs.
func entrypointFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read module entrypoint directory: %w", err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".dang" {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	return files, nil
}

func entrypointResourcesDecls(file *dang.FileBlock) []*dang.FieldDecl {
	var decls []*dang.FieldDecl
	for _, form := range file.Forms {
		decl, ok := form.(*dang.ObjectDecl)
		if !ok || decl.Value == nil || !implementsModuleEntrypoint(decl) {
			continue
		}
		for _, member := range decl.Value.Forms {
			field, ok := member.(*dang.FieldDecl)
			if ok && field.Name != nil && field.Name.Name == EntrypointResourcesMember && field.Visibility >= dang.PublicVisibility {
				decls = append(decls, field)
			}
		}
	}
	return decls
}

// checkEntrypointResourcesDecl refuses a value field, which Dang turns into a
// constructor argument, and a required argument, which the engine never passes.
func checkEntrypointResourcesDecl(decl *dang.FieldDecl) error {
	fn, ok := decl.Value.(*dang.FunDecl)
	if !ok {
		return fmt.Errorf("%s must be a function: declare it as pub %s: [String!]! { ... }", EntrypointResourcesMember, EntrypointResourcesMember)
	}
	for _, arg := range fn.Args {
		if _, nonNull := arg.Type_.(dang.NonNullTypeNode); nonNull && arg.Value == nil {
			return fmt.Errorf("%s must not require an argument: the engine passes none, but %q is required", EntrypointResourcesMember, arg.Name.Name)
		}
	}
	return nil
}

func implementsModuleEntrypoint(decl *dang.ObjectDecl) bool {
	for _, iface := range decl.Implements {
		if iface != nil && iface.Name == moduleEntrypointTypeName {
			return true
		}
	}
	return false
}

// EntrypointResources returns what the resources member of the entrypoint in
// src's context directory returns, and false when the entrypoint declares no
// such member. The program is checked against schemaFile but gets no working
// client, so its result depends on its own text only.
func EntrypointResources(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	schemaFile dagql.ObjectResult[*core.File],
) (patterns []string, declared bool, rerr error) {
	var intro introspection.Response
	f, err := schemaFile.Self().Open(ctx, schemaFile)
	if err != nil {
		return nil, false, fmt.Errorf("open schema file: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&intro); err != nil {
		return nil, false, fmt.Errorf("decode schema: %w", err)
	}

	contextDir := src.Self().ContextDirectory
	err = contextDir.Self().Mount(ctx, contextDir, func(path string) error {
		dir := filepath.Join(path, src.Self().SourceSubpath)
		declared, err = DeclaresEntrypointResources(dir)
		if err != nil || !declared {
			return err
		}
		ensureModuleSelfTypes(intro.Schema, src.Self(), dir)
		patterns, err = evalEntrypointResources(ctx, intro.Schema, refusingClient{}, dir, dangModule{
			name:         src.Self().ModuleName,
			originalName: src.Self().ModuleOriginalName,
		})
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return patterns, declared, nil
}

func evalEntrypointResources(
	ctx context.Context,
	schema *introspection.Schema,
	client graphql.Client,
	dir string,
	module dangModule,
) ([]string, error) {
	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
		Name:       "Dagger",
		Client:     client,
		Schema:     schema,
		AutoImport: true,
	})
	env, err := runEntrypointDir(ctx, dir)
	if err != nil {
		if isDangSourceError(err) {
			return nil, &dangSourceError{err: err}
		}
		return nil, fmt.Errorf("run module entrypoint: %w", err)
	}
	entrypointName, err := findModuleEntrypoint(env)
	if err != nil {
		return nil, err
	}
	value, err := callDangFunction(ctx, env, &core.FunctionCall{
		Name:       EntrypointResourcesMember,
		ParentName: entrypointName,
		Parent:     core.JSON("{}"),
	}, module)
	if err != nil {
		return nil, err
	}
	return entrypointResourcesList(value)
}

func entrypointResourcesList(value dang.Value) ([]string, error) {
	list, ok := value.(dang.ListValue)
	if !ok {
		return nil, fmt.Errorf("%s returned %T instead of a [String!]! list", EntrypointResourcesMember, value)
	}
	patterns := make([]string, 0, len(list.Elements))
	for i, element := range list.Elements {
		str, ok := element.(dang.StringValue)
		if !ok {
			return nil, fmt.Errorf("%s element %d is %T instead of a string", EntrypointResourcesMember, i, element)
		}
		patterns = append(patterns, str.Val)
	}
	return patterns, nil
}
