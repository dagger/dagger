package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// ModuleNaming names a module's own declarations as the engine does once it
// loads the module, from the module source alone: its name, original name and
// engine version (ModuleSource.NamingView). SDK codegen uses it to spell the
// module's schema names before the module is loaded.
type ModuleNaming struct {
	// View is the API view whose naming rules name the module
	// (ModuleSource.NamingView).
	View call.View
	// ModuleName is the module's final name, which namespaces its types.
	ModuleName string
	// ModuleOriginalName is the module's intrinsic name, which its main
	// object and namespaced type names are spelled after.
	ModuleOriginalName string
}

func (*ModuleNaming) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleNaming",
		NonNull:   true,
	}
}

func (*ModuleNaming) TypeDescription() string {
	return dagql.FormatDescription(
		`The names the engine gives a module's own declarations, by the naming rules of the version the module is named by.`,
		`Every list-taking field answers in input order.`)
}

// Naming returns how the engine names src's own declarations: by the rules of
// its NamingView, under its name and original name. It reads nothing else of
// the source, so it answers before the module's entrypoint is generated.
func (src *ModuleSource) Naming() *ModuleNaming {
	return &ModuleNaming{
		View:               src.NamingView(),
		ModuleName:         src.ModuleName,
		ModuleOriginalName: src.ModuleOriginalName,
	}
}

// Namer returns the rules that name the module.
func (mn *ModuleNaming) Namer() Namer {
	return NamerForView(mn.View)
}

// mainName is the name the module finds its main object by: its original
// name, else its final name (see Module.MainObject).
func (mn *ModuleNaming) mainName() string {
	if mn.ModuleOriginalName != "" {
		return mn.ModuleOriginalName
	}
	return mn.ModuleName
}

// names normalizes each name with normalize, in order, after checking the
// rules can name it (Namer.Validate).
func (mn *ModuleNaming) names(names []string, normalize func(Namer, string) string) ([]string, error) {
	namer := mn.Namer()
	out := make([]string, len(names))
	for i, name := range names {
		if err := namer.Validate(name); err != nil {
			return nil, err
		}
		out[i] = normalize(namer, name)
	}
	return out, nil
}

// TypeNames are the schema names of objects, interfaces and enums the module
// declares: namespaced under the module, as the engine installs them
// (Namer.NamespaceObject).
func (mn *ModuleNaming) TypeNames(names []string) ([]string, error) {
	return mn.names(names, func(n Namer, name string) string {
		return n.NamespaceObject(name, mn.ModuleName, mn.ModuleOriginalName)
	})
}

// FieldNames are the schema names of functions, fields and arguments the
// module declares (Namer.FieldName, Namer.ArgName).
func (mn *ModuleNaming) FieldNames(names []string) ([]string, error) {
	return mn.names(names, Namer.FieldName)
}

// EnumMemberNames are the schema names of enum members the module declares
// (Namer.EnumMemberName).
func (mn *ModuleNaming) EnumMemberNames(names []string) ([]string, error) {
	return mn.names(names, Namer.EnumMemberName)
}

// MainObject returns which of candidates, the names of objects the module
// declares, the engine takes as its main object, as it finds it by the
// module's name (Namer.DeclaredNameIndex); false if none or several flat
// matches.
func (mn *ModuleNaming) MainObject(candidates []string) (string, bool, error) {
	namer := mn.Namer()
	for _, name := range candidates {
		if err := namer.Validate(name); err != nil {
			return "", false, err
		}
	}
	i := namer.DeclaredNameIndex(mn.mainName(), candidates)
	if i < 0 {
		return "", false, nil
	}
	return candidates[i], true, nil
}

type persistedModuleNamingPayload struct {
	View               string `json:"view,omitempty"`
	ModuleName         string `json:"moduleName,omitempty"`
	ModuleOriginalName string `json:"moduleOriginalName,omitempty"`
}

func (mn *ModuleNaming) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if mn == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted module naming: nil module naming")
	}
	return encodePersistedObjectPayload(persistedModuleNamingPayload{
		View:               string(mn.View),
		ModuleName:         mn.ModuleName,
		ModuleOriginalName: mn.ModuleOriginalName,
	})
}

func (*ModuleNaming) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedModuleNamingPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted module naming payload: %w", err)
	}
	return &ModuleNaming{
		View:               call.View(persisted.View),
		ModuleName:         persisted.ModuleName,
		ModuleOriginalName: persisted.ModuleOriginalName,
	}, nil
}
