package dangv2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/introspection"

	"github.com/dagger/dagger/core"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/core/sdk/entrypoint"
	"github.com/dagger/dagger/dagql"
)

// EntrypointSourcesMember is the optional entrypoint member that returns the
// files the module builds from. Declaring it selects the entrypoint contract
// in which types and call receive no workspace.
const EntrypointSourcesMember = "sources"

// DeclaresEntrypointSources reports whether a type that implements
// ModuleEntrypoint in the top-level Dang files of dir declares a public
// sources member. A comment, a string, a private member or a member of another
// type does not count. A file that does not parse declares nothing, so its
// error surfaces when the entrypoint runs.
func DeclaresEntrypointSources(dir string) (bool, error) {
	files, err := entrypointFiles(dir)
	if err != nil {
		return false, err
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("read module entrypoint file %q: %w", filepath.Base(path), err)
		}
		if !bytes.Contains(data, []byte(EntrypointSourcesMember)) {
			continue
		}
		root, err := dang.Parse(path, data)
		if err != nil {
			return false, nil //nolint:nilerr
		}
		if file, ok := root.(*dang.FileBlock); ok && declaresSources(file) {
			return true, nil
		}
	}
	return false, nil
}

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

func declaresSources(file *dang.FileBlock) bool {
	for _, form := range file.Forms {
		decl, ok := form.(*dang.ObjectDecl)
		if !ok || decl.Value == nil || !implementsModuleEntrypoint(decl) {
			continue
		}
		for _, member := range decl.Value.Forms {
			field, ok := member.(*dang.FieldDecl)
			if ok && field.Name != nil && field.Name.Name == EntrypointSourcesMember && field.Visibility >= dang.PublicVisibility {
				return true
			}
		}
	}
	return false
}

func implementsModuleEntrypoint(decl *dang.ObjectDecl) bool {
	for _, iface := range decl.Implements {
		if iface != nil && iface.Name == moduleEntrypointTypeName {
			return true
		}
	}
	return false
}

// EntrypointSources calls the sources member of the entrypoint in entryDir
// with workspace and returns the Directory it returns. No module exists yet,
// so the program runs without currentModule. Its nested client has what a
// module function has: the Dagger API, and the host only through the
// workspace it is given.
func EntrypointSources(
	ctx context.Context,
	src *core.ModuleSource,
	entryDir dagql.ObjectResult[*core.Directory],
	schemaFile dagql.Result[*core.File],
	workspace dagql.ObjectResult[*core.Workspace],
) (dagql.ObjectResult[*core.Directory], error) {
	var sources dagql.ObjectResult[*core.Directory]
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return sources, err
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return sources, err
	}
	var intro introspection.Response
	f, err := schemaFile.Self().Open(ctx, dagql.ObjectResult[*core.File]{Result: schemaFile})
	if err != nil {
		return sources, fmt.Errorf("open schema file: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&intro); err != nil {
		return sources, fmt.Errorf("decode schema: %w", err)
	}
	workspaceArg, err := entrypoint.WorkspaceCallArg(workspace)
	if err != nil {
		return sources, err
	}
	nestedClientMetadata, err := dangshared.NewNestedClientMetadata(ctx)
	if err != nil {
		return sources, err
	}
	module := dangModule{name: src.ModuleName, originalName: src.ModuleOriginalName}

	_, err = dangshared.WithNestedClientServer(ctx, query, nestedClientMetadata, true, nil, dagql.ObjectResult[*core.Module]{}, func(ctx context.Context, client graphql.Client) ([]byte, error) {
		ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
			Name:       "Dagger",
			Client:     client,
			Schema:     intro.Schema,
			AutoImport: true,
		})
		return nil, entryDir.Self().Mount(ctx, entryDir, func(dir string) error {
			ensureModuleSelfTypes(intro.Schema, src, dir)
			env, err := runEntrypointDir(ctx, dir, true)
			if err != nil {
				if isDangSourceError(err) {
					return &dangSourceError{err: err}
				}
				return fmt.Errorf("run module entrypoint: %w", err)
			}
			entrypointName, err := findModuleEntrypoint(env)
			if err != nil {
				return err
			}
			value, err := callDangFunction(ctx, env, &core.FunctionCall{
				Name:       EntrypointSourcesMember,
				ParentName: entrypointName,
				Parent:     core.JSON("{}"),
				InputArgs:  []*core.FunctionCallArgValue{workspaceArg},
			}, module)
			if err != nil {
				return err
			}
			sources, err = loadEntrypointDirectory(ctx, dag, value)
			return err
		})
	})
	if err != nil {
		return sources, dangshared.ConvertError(err)
	}
	return sources, nil
}

func loadEntrypointDirectory(ctx context.Context, dag *dagql.Server, value dang.Value) (dagql.ObjectResult[*core.Directory], error) {
	var directory dagql.ObjectResult[*core.Directory]
	object, ok := value.(dang.GraphQLValue)
	if !ok || object.TypeName != "Directory" {
		return directory, fmt.Errorf("%s returned %T instead of a Directory", EntrypointSourcesMember, value)
	}
	encoded, err := object.ID(ctx)
	if err != nil {
		return directory, fmt.Errorf("get %s directory ID: %w", EntrypointSourcesMember, err)
	}
	var id dagql.ID[*core.Directory]
	if err := id.Decode(encoded); err != nil {
		return directory, fmt.Errorf("decode %s directory ID: %w", EntrypointSourcesMember, err)
	}
	return id.Load(ctx, dag)
}
