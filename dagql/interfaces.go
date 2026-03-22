package dagql

import (
	"context"
	"fmt"
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

// FieldSpec looks up a field spec by name for the given view.
func (iface *Interface) FieldSpec(name string, view call.View) (FieldSpec, bool) {
	iface.fieldsL.Lock()
	defer iface.fieldsL.Unlock()
	versions, ok := iface.fields[name]
	if !ok {
		return FieldSpec{}, false
	}
	for i := len(versions) - 1; i >= 0; i-- {
		f := versions[i]
		if f.MinVersion == "" || f.MinVersion == view {
			return f.FieldSpec, true
		}
	}
	return FieldSpec{}, false
}

// ParseField parses an AST field selection against this interface's field specs.
// This is used when a query selects fields on an interface-typed return value.
func (iface *Interface) ParseField(ctx context.Context, view call.View, astField *ast.Field, vars map[string]any) (Selector, *ast.Type, error) {
	spec, ok := iface.FieldSpec(astField.Name, view)
	if !ok {
		return Selector{}, nil, fmt.Errorf("%s has no such field: %q", iface.name, astField.Name)
	}
	args := make([]NamedInput, len(astField.Arguments))
	for i, arg := range astField.Arguments {
		argSpec, ok := spec.Args.Input(arg.Name, view)
		if !ok {
			return Selector{}, nil, fmt.Errorf("%s.%s has no such argument: %q", iface.name, spec.Name, arg.Name)
		}
		if argSpec.Internal {
			return Selector{}, nil, fmt.Errorf("cannot use internal argument %q in selector for %s.%s", arg.Name, iface.name, spec.Name)
		}
		val, err := arg.Value.Value(vars)
		if err != nil {
			return Selector{}, nil, err
		}
		input, err := argSpec.Type.Decoder().DecodeInput(val)
		if err != nil {
			return Selector{}, nil, fmt.Errorf("init arg %q value as %T (%s) using %T: %w", arg.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
		}
		args[i] = NamedInput{
			Name:  arg.Name,
			Value: input,
		}
	}
	return Selector{
		Field: astField.Name,
		Args:  args,
		View:  view,
	}, spec.Type.Type(), nil
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
//
// The optional implementsChecker allows covariant return type checking: if the
// interface declares `foo: SomeIface` and the object has `foo: ConcreteObj`,
// the checker verifies that ConcreteObj implements SomeIface.
func (iface *Interface) Satisfies(obj ObjectType, view call.View, checkers ...ImplementsChecker) bool {
	var checker ImplementsChecker
	if len(checkers) > 0 {
		checker = checkers[0]
	}
	for _, ifaceField := range iface.FieldSpecs(view) {
		objField, ok := obj.FieldSpec(ifaceField.Name, view)
		if !ok {
			return false
		}
		if !typeCompatible(ifaceField.Type.Type(), objField.Type.Type(), checker) {
			return false
		}
		// Note: argument contravariance checking is intentionally omitted for
		// Phase 1. Core can still layer on its own semantic rules.
	}
	return true
}

// ImplementsChecker is a function that checks whether a type (by name) implements
// a given interface (by name). This is used for covariant return type checking.
type ImplementsChecker func(typeName, ifaceName string) bool

// typeCompatible checks whether the object's return type is compatible with
// the interface's declared return type.
//
// With a checker, covariant return types are allowed: if the interface declares
// return type "Foo" and the object returns "Bar", the check passes if Bar
// implements Foo.
func typeCompatible(ifaceType, objType *ast.Type, checker ImplementsChecker) bool {
	if ifaceType == nil || objType == nil {
		return ifaceType == objType
	}
	// list types
	if ifaceType.Elem != nil || objType.Elem != nil {
		if ifaceType.Elem == nil || objType.Elem == nil {
			return false
		}
		return typeCompatible(ifaceType.Elem, objType.Elem, checker)
	}
	// named types: exact match
	if ifaceType.NamedType == objType.NamedType {
		// obj returning NonNull satisfies iface declaring nullable (covariant)
		if ifaceType.NonNull && !objType.NonNull {
			return false
		}
		return true
	}
	// covariant return: obj returns a subtype of the interface's declared type
	if checker != nil && checker(objType.NamedType, ifaceType.NamedType) {
		if ifaceType.NonNull && !objType.NonNull {
			return false
		}
		return true
	}
	return false
}

// addImplementor records that an object type implements this interface.
func (iface *Interface) addImplementor(typeName string) {
	iface.implementors[typeName] = struct{}{}
}

// Implementors returns the set of type names that implement this interface.
func (iface *Interface) Implementors() map[string]struct{} {
	return iface.implementors
}
