package dagql

import (
	"context"
	"fmt"
	"slices"
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

	// relationsL protects the schema relationships that can change after an
	// interface has been installed. Query parsing reads implementors to decide
	// whether fragment type conditions apply, while module schema construction can
	// register implementations concurrently for dependency-aware schemas.
	relationsL *sync.RWMutex
	directives []*ast.Directive

	// implementors tracks which object types implement this interface.
	// Keys are type names.
	implementors map[string]struct{}

	// interfaces tracks which other interfaces this interface implements.
	// Keys are interface names.
	interfaces map[string]*Interface
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
		relationsL:   new(sync.RWMutex),
		implementors: make(map[string]struct{}),
		interfaces:   make(map[string]*Interface),
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
		fieldDef := spec.FieldDefinition(view)
		// For non-"id" fields that return the ID scalar without an
		// @expectedType directive, add @expectedType pointing to this
		// interface. This tells SDKs that the field returns "an ID of
		// something that implements this interface", enabling covariant
		// return types (e.g. Container.sync() returns Container, which
		// is a subtype of Syncer).
		if spec.Name != "id" && fieldDef.Type.Name() == "ID" {
			hasExpectedType := slices.ContainsFunc(fieldDef.Directives,
				func(d *ast.Directive) bool { return d.Name == "expectedType" })
			if !hasExpectedType {
				fieldDef.Directives = append(fieldDef.Directives, ExpectedTypeDirective(iface.name))
			}
		}
		def.Fields = append(def.Fields, fieldDef)
	}

	// Declare which other interfaces this interface implements.
	iface.relationsL.RLock()
	for ifaceName := range iface.interfaces {
		def.Interfaces = append(def.Interfaces, ifaceName)
	}
	if len(iface.directives) > 0 {
		def.Directives = append(def.Directives, iface.directives...)
	}
	iface.relationsL.RUnlock()
	sort.Strings(def.Interfaces)

	return def
}

// Satisfies returns true if the given object type structurally satisfies this
// interface — i.e. it has all fields required by the interface with compatible
// return types and arguments.
//
// The optional implementsChecker allows covariant return type checking: if the
// interface declares `foo: SomeIface` and the object has `foo: ConcreteObj`,
// the checker verifies that ConcreteObj implements SomeIface. The same checker
// is used in reverse for contravariant argument type checking.
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
		if !argsCompatible(ifaceField.Args, objField.Args, view, checker) {
			return false
		}
	}
	return true
}

// SatisfiedByInterface returns true if the given interface structurally satisfies
// this interface — i.e. it has all fields required by this interface with
// compatible return types and arguments.
//
// The optional implementsChecker allows covariant return type checking and
// contravariant argument type checking, same as Satisfies.
func (iface *Interface) SatisfiedByInterface(other *Interface, view call.View, checkers ...ImplementsChecker) bool {
	var checker ImplementsChecker
	if len(checkers) > 0 {
		checker = checkers[0]
	}
	for _, ifaceField := range iface.FieldSpecs(view) {
		otherField, ok := other.FieldSpec(ifaceField.Name, view)
		if !ok {
			return false
		}
		if !typeCompatible(ifaceField.Type.Type(), otherField.Type.Type(), checker) {
			return false
		}
		if !argsCompatible(ifaceField.Args, otherField.Args, view, checker) {
			return false
		}
	}
	return true
}

// ImplementsChecker is a function that checks whether a type (by name) implements
// a given interface (by name). This is used for covariant return type checking.
type ImplementsChecker func(typeName, ifaceName string) bool

// argsCompatible checks whether the implementing type's arguments are compatible
// with the interface's declared arguments.
//
// Every argument declared by the interface must be present on the implementing
// type with a compatible type. Extra arguments on the implementing type are
// allowed (they must have defaults or be optional).
//
// Argument types are checked with contravariant rules: the implementing type's
// argument may accept a wider (more permissive) type than the interface declares.
func argsCompatible(ifaceArgs, objArgs InputSpecs, view call.View, checker ImplementsChecker) bool {
	for _, ifaceArg := range ifaceArgs.Inputs(view) {
		objArg, ok := objArgs.Input(ifaceArg.Name, view)
		if !ok {
			return false
		}
		if !argTypeCompatible(ifaceArg.Type.Type(), objArg.Type.Type(), checker) {
			return false
		}
	}
	return true
}

// argTypeCompatible checks whether an implementing type's argument type is
// compatible with the interface's declared argument type.
//
// This applies contravariant rules:
//   - Type names must match (or the interface's arg type must implement the
//     object's arg type — the reverse of covariant return type checking)
//   - The implementing type's argument may be nullable when the interface
//     requires non-null (accepting more values), but not vice versa
func argTypeCompatible(ifaceArgType, objArgType *ast.Type, checker ImplementsChecker) bool {
	if ifaceArgType == nil || objArgType == nil {
		return ifaceArgType == objArgType
	}
	// list types
	if ifaceArgType.Elem != nil || objArgType.Elem != nil {
		if ifaceArgType.Elem == nil || objArgType.Elem == nil {
			return false
		}
		return argTypeCompatible(ifaceArgType.Elem, objArgType.Elem, checker)
	}
	// named types: exact match
	if ifaceArgType.NamedType == objArgType.NamedType {
		// Contravariant nullability: object arg requiring NonNull when interface
		// allows nullable is too restrictive.
		if objArgType.NonNull && !ifaceArgType.NonNull {
			return false
		}
		return true
	}
	// contravariant: interface's arg type is a subtype of object's arg type.
	// This is the reverse of covariant return type checking — the implementing
	// type accepts a wider input type.
	if checker != nil && checker(ifaceArgType.NamedType, objArgType.NamedType) {
		if objArgType.NonNull && !ifaceArgType.NonNull {
			return false
		}
		return true
	}
	return false
}

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

func (iface *Interface) addDirectives(directives ...*ast.Directive) {
	iface.relationsL.Lock()
	defer iface.relationsL.Unlock()
	iface.directives = append(iface.directives, directives...)
}

// addImplementor records that an object type implements this interface.
func (iface *Interface) addImplementor(typeName string) {
	iface.relationsL.Lock()
	defer iface.relationsL.Unlock()
	iface.implementors[typeName] = struct{}{}
}

// HasImplementor returns true if the named object or interface type implements
// this interface.
func (iface *Interface) HasImplementor(typeName string) bool {
	iface.relationsL.RLock()
	defer iface.relationsL.RUnlock()
	_, ok := iface.implementors[typeName]
	return ok
}

// ImplementInterface declares that this interface implements another interface.
// This is the interface-to-interface equivalent of Class.Implements.
func (iface *Interface) ImplementInterface(other *Interface) {
	iface.relationsL.Lock()
	iface.interfaces[other.TypeName()] = other
	iface.relationsL.Unlock()

	// Also register this interface as an implementor of the other interface,
	// so possibleTypes includes it.
	other.addImplementor(iface.name)
}

// Implementors returns a snapshot of the type names that implement this interface.
func (iface *Interface) Implementors() map[string]struct{} {
	iface.relationsL.RLock()
	defer iface.relationsL.RUnlock()
	implementors := make(map[string]struct{}, len(iface.implementors))
	for typeName := range iface.implementors {
		implementors[typeName] = struct{}{}
	}
	return implementors
}

// Interfaces returns a snapshot of the interfaces that this interface implements.
func (iface *Interface) Interfaces() map[string]*Interface {
	iface.relationsL.RLock()
	defer iface.relationsL.RUnlock()
	interfaces := make(map[string]*Interface, len(iface.interfaces))
	for name, implemented := range iface.interfaces {
		interfaces[name] = implemented
	}
	return interfaces
}
