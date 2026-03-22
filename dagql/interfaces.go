package dagql

import (
	"sort"
	"sync"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

// Interface represents a GraphQL Interface type in dagql.
//
// Unlike Class (which represents object types), an Interface only declares
// field *specs* — it has no resolvers of its own. Concrete objects that
// implement the interface provide the actual field implementations.
type Interface struct {
	name        string
	description string
	fields      map[string][]*InterfaceFieldSpec
	fieldsL     *sync.Mutex
	directives  []*ast.Directive

	// implementors tracks which object types implement this interface.
	// Keys are type names.
	implementors map[string]struct{}
}

// InterfaceFieldSpec pairs a FieldSpec with optional view gating,
// mirroring how object fields work.
type InterfaceFieldSpec struct {
	FieldSpec
	MinVersion call.View // view gating, same as object fields
}

// NewInterface creates a new Interface with the given name and description.
func NewInterface(name, description string) *Interface {
	return &Interface{
		name:         name,
		description:  description,
		fields:       make(map[string][]*InterfaceFieldSpec),
		fieldsL:      new(sync.Mutex),
		implementors: make(map[string]struct{}),
	}
}

// TypeName returns the name of the interface type.
func (iface *Interface) TypeName() string {
	return iface.name
}

// TypeDescription returns the description of the interface type.
func (iface *Interface) TypeDescription() string {
	return iface.description
}

// AddField adds a field spec to the interface.
func (iface *Interface) AddField(spec InterfaceFieldSpec) {
	iface.fieldsL.Lock()
	defer iface.fieldsL.Unlock()
	iface.fields[spec.Name] = append(iface.fields[spec.Name], &spec)
}

// FieldSpecs returns the interface's field specs visible under the given view.
func (iface *Interface) FieldSpecs(view call.View) []FieldSpec {
	iface.fieldsL.Lock()
	defer iface.fieldsL.Unlock()

	var specs []FieldSpec
	seen := map[string]struct{}{}
	for name, versions := range iface.fields {
		if _, ok := seen[name]; ok {
			continue
		}
		// take the last matching version (same precedence logic as Class)
		for i := len(versions) - 1; i >= 0; i-- {
			f := versions[i]
			if f.MinVersion == "" || f.MinVersion == view {
				specs = append(specs, f.FieldSpec)
				seen[name] = struct{}{}
				break
			}
		}
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})
	return specs
}

// Definition returns the ast.Definition for this interface for the given view.
func (iface *Interface) Definition(view call.View) *ast.Definition {
	def := &ast.Definition{
		Kind:        ast.Interface,
		Name:        iface.name,
		Description: iface.description,
	}

	for _, spec := range iface.FieldSpecs(view) {
		def.Fields = append(def.Fields, spec.FieldDefinition(view))
	}

	if len(iface.directives) > 0 {
		def.Directives = append(def.Directives, iface.directives...)
	}

	return def
}

// Satisfies returns true if the given object type structurally satisfies this
// interface — i.e. it has all fields required by the interface with compatible
// return types.
func (iface *Interface) Satisfies(obj ObjectType, view call.View) bool {
	for _, ifaceField := range iface.FieldSpecs(view) {
		objField, ok := obj.FieldSpec(ifaceField.Name, view)
		if !ok {
			return false
		}
		if !typeCompatible(ifaceField.Type.Type(), objField.Type.Type()) {
			return false
		}
		// Note: argument contravariance checking is intentionally omitted for
		// Phase 1. Core can still layer on its own semantic rules.
	}
	return true
}

// typeCompatible checks whether the object's return type is compatible with
// the interface's declared return type. For now this is a simple name check
// that also allows non-null object types to satisfy nullable interface types.
func typeCompatible(ifaceType, objType *ast.Type) bool {
	if ifaceType == nil || objType == nil {
		return ifaceType == objType
	}
	// list types
	if ifaceType.Elem != nil || objType.Elem != nil {
		if ifaceType.Elem == nil || objType.Elem == nil {
			return false
		}
		return typeCompatible(ifaceType.Elem, objType.Elem)
	}
	// named types: names must match
	if ifaceType.NamedType != objType.NamedType {
		return false
	}
	// obj returning NonNull satisfies iface declaring nullable (covariant)
	if ifaceType.NonNull && !objType.NonNull {
		return false
	}
	return true
}

// addImplementor records that an object type implements this interface.
func (iface *Interface) addImplementor(typeName string) {
	iface.implementors[typeName] = struct{}{}
}

// Implementors returns the set of type names that implement this interface.
func (iface *Interface) Implementors() map[string]struct{} {
	return iface.implementors
}
