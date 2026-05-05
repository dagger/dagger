package schematool

import (
	"fmt"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func mergeInto(schema *introspection.Schema, mod *ModuleTypes) error {
	if mod == nil {
		return fmt.Errorf("module types: nil")
	}
	if alreadyMergedFor(schema, mod.Name) {
		// Idempotent: this module's types are already present on the
		// schema. The multi-pass codegen loop reuses the same schema
		// pointer across iterations, so re-merging the same module
		// must be a no-op rather than a collision error.
		return nil
	}
	for _, obj := range mod.Objects {
		if schema.Types.Get(obj.Name) != nil {
			return fmt.Errorf("type %q already exists in schema", obj.Name)
		}
		schema.Types = append(schema.Types, convertObject(obj, mod.Name))
	}
	for _, iface := range mod.Interfaces {
		if schema.Types.Get(iface.Name) != nil {
			return fmt.Errorf("type %q already exists in schema", iface.Name)
		}
		schema.Types = append(schema.Types, convertInterface(iface, mod.Name))
	}
	for _, enum := range mod.Enums {
		if schema.Types.Get(enum.Name) != nil {
			return fmt.Errorf("type %q already exists in schema", enum.Name)
		}
		schema.Types = append(schema.Types, convertEnum(enum, mod.Name))
	}
	return registerModuleConstructor(schema, mod)
}

// registerModuleConstructor adds a field on the Query type that
// returns the module's main object. This is what lets clients build
// the module (e.g., `dag.MyModule()` in Go) — the Query field is
// the same shape the engine's module-install path would produce when
// registering a module as a dependency.
//
// The main object is the one whose name matches the module name in
// PascalCase (e.g., module "my-module" → object "MyModule"). If
// there's no matching object, Merge still succeeds with no Query
// field added.
func registerModuleConstructor(schema *introspection.Schema, mod *ModuleTypes) error {
	mainObjectName := strcase.ToCamel(mod.Name)
	var mainObj *ObjectDef
	for i := range mod.Objects {
		if mod.Objects[i].Name == mainObjectName {
			mainObj = &mod.Objects[i]
			break
		}
	}
	if mainObj == nil {
		return nil
	}

	queryType := schema.Types.Get("Query")
	if queryType == nil {
		return fmt.Errorf("query type not found in schema")
	}

	fieldName := strcase.ToLowerCamel(mod.Name)
	if queryFieldExists(queryType, fieldName) {
		// Idempotent: the field is already registered (schema was
		// merged previously during the same codegen invocation).
		return nil
	}

	field := &introspection.Field{
		Name:        fieldName,
		Description: mainObj.Description,
		TypeRef: &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind: introspection.TypeKindObject,
				Name: mainObj.Name,
			},
		},
		Args:       introspection.InputValues{},
		Directives: moduleDirectives(mod.Name),
	}

	if mainObj.Constructor != nil {
		for _, a := range mainObj.Constructor.Args {
			iv := introspection.InputValue{
				Name:        a.Name,
				Description: a.Description,
				TypeRef:     convertTypeRef(a.TypeRef),
				Directives:  introspection.Directives{},
			}
			if a.DefaultValue != nil {
				iv.DefaultValue = a.DefaultValue
			}
			field.Args = append(field.Args, iv)
		}
	}

	queryType.Fields = append(queryType.Fields, field)
	return nil
}

func queryFieldExists(queryType *introspection.Type, name string) bool {
	for _, f := range queryType.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// alreadyMergedFor returns true if schema already contains a type
// stamped with `@sourceModuleName(name: "<modName>")`. If yes, we
// assume the merge has already happened on this schema instance and
// skip it to keep Merge idempotent across the multi-pass codegen
// loop. Genuine collisions (types whose name matches but carry no
// sourceModuleName, or a different sourceModuleName) fall through to
// the per-type checks below and still error.
func alreadyMergedFor(schema *introspection.Schema, modName string) bool {
	want := fmt.Sprintf("%q", modName)
	for _, t := range schema.Types {
		d := t.Directives.Directive("sourceModuleName")
		if d == nil {
			continue
		}
		v := d.Arg("name")
		if v != nil && *v == want {
			return true
		}
	}
	return false
}

func convertObject(obj ObjectDef, modName string) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindObject,
		Name:        obj.Name,
		Description: obj.Description,
		Fields:      []*introspection.Field{},
		Interfaces:  []*introspection.Type{},
		Directives:  moduleDirectives(modName),
	}
	for _, fn := range obj.Functions {
		t.Fields = append(t.Fields, convertFunction(fn))
	}
	for _, f := range obj.Fields {
		t.Fields = append(t.Fields, &introspection.Field{
			Name:        f.Name,
			Description: f.Description,
			TypeRef:     convertTypeRef(f.TypeRef),
			Args:        introspection.InputValues{},
			Directives:  introspection.Directives{},
		})
	}
	return t
}

func convertInterface(iface InterfaceDef, modName string) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindInterface,
		Name:        iface.Name,
		Description: iface.Description,
		Fields:      []*introspection.Field{},
		Interfaces:  []*introspection.Type{},
		Directives:  moduleDirectives(modName),
	}
	for _, fn := range iface.Functions {
		t.Fields = append(t.Fields, convertFunction(fn))
	}
	return t
}

func convertEnum(enum EnumDef, modName string) *introspection.Type {
	t := &introspection.Type{
		Kind:        introspection.TypeKindEnum,
		Name:        enum.Name,
		Description: enum.Description,
		EnumValues:  []introspection.EnumValue{},
		Interfaces:  []*introspection.Type{},
		Directives:  moduleDirectives(modName),
	}
	for _, v := range enum.Values {
		t.EnumValues = append(t.EnumValues, introspection.EnumValue{
			Name:        v.Name,
			Description: v.Description,
			Directives:  introspection.Directives{},
		})
	}
	return t
}

func convertFunction(fn Function) *introspection.Field {
	f := &introspection.Field{
		Name:        fn.Name,
		Description: fn.Description,
		TypeRef:     convertTypeRef(fn.ReturnType),
		Args:        introspection.InputValues{},
		Directives:  introspection.Directives{},
	}
	for _, a := range fn.Args {
		iv := introspection.InputValue{
			Name:        a.Name,
			Description: a.Description,
			TypeRef:     convertTypeRef(a.TypeRef),
			Directives:  introspection.Directives{},
		}
		if a.DefaultValue != nil {
			iv.DefaultValue = a.DefaultValue
		}
		f.Args = append(f.Args, iv)
	}
	return f
}

func convertTypeRef(ref *TypeRef) *introspection.TypeRef {
	if ref == nil {
		return nil
	}
	return &introspection.TypeRef{
		Kind:   introspection.TypeKind(ref.Kind),
		Name:   ref.Name,
		OfType: convertTypeRef(ref.OfType),
	}
}

// moduleDirectives builds the directives stamped on every type
// inserted by Merge. Both `@sourceModuleName(name:)` (engine
// historical marker, also used by alreadyMergedFor) and
// `@sourceMap(module:)` are emitted: the latter is what
// codegen file-splitting relies on (Schema.DependencyNames /
// Include / Exclude all read it), so its presence is what causes
// self-call types to land in their own per-module .gen.go file.
//
// Directive arg values are JSON-encoded to mirror how the engine's
// introspection responses carry them (quoted strings, not bare
// identifiers).
func moduleDirectives(modName string) introspection.Directives {
	value := fmt.Sprintf("%q", modName)
	return introspection.Directives{
		{
			Name: "sourceModuleName",
			Args: []*introspection.DirectiveArg{
				{Name: "name", Value: &value},
			},
		},
		{
			Name: "sourceMap",
			Args: []*introspection.DirectiveArg{
				{Name: "module", Value: &value},
			},
		},
	}
}
