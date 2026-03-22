# First-Class Interfaces in DagQL

## Problem

DagQL has no concept of GraphQL interfaces. Dagger bolts interfaces on top of
regular objects:

1. **Interfaces are objects.** `InterfaceAnnotatedValue` is a dagql `Typed`
   whose `TypeDefinition()` returns `Kind: ast.Object` — not `ast.Interface`.

2. **`asFoo` conversion fields.** `ModDeps.lazilyLoadSchema` adds an
   `asFoo: Foo!` field to every object that implements interface `Foo`. This is
   the only way to "upcast" from a concrete object to an interface.

3. **Subtype checking lives in core.** `ObjectTypeDef.IsSubtypeOf`,
   `Function.IsSubtypeOf`, and `TypeDef.IsSubtypeOf` implement
   covariant/contravariant type checking — but dagql knows nothing about any of
   it.

4. **No fragment support.** `parseASTSelections` handles `*ast.Field` and
   `*ast.FragmentSpread` but not `*ast.InlineFragment`. Fragment spreads don't
   check type conditions.

5. **`DynamicID` + `InterfaceAnnotatedValue`.** Interface values are wrapped in
   `InterfaceAnnotatedValue` (which implements `dagql.InterfaceValue`), and
   dagql has a special `toSelectable` path that unwraps it. The wrapping and
   unwrapping is fragile and adds complexity.

6. **No `implements` in the schema.** Object definitions never declare which
   interfaces they implement, so introspection doesn't expose this
   information.

## Goals

- DagQL should be aware of interfaces and treat them as first-class: defining
  them, recording which objects implement them, generating the correct
  `ast.Interface` definitions, and populating `PossibleTypes`.
- Core should define `interface Object { id: ID! }` — the root interface that
  every object implicitly implements.
- The `asFoo` conversion fields should be eliminated at the dagql layer.
  Objects that implement an interface are directly usable in interface-typed
  positions.
- Subtype checking should move into dagql (at least structurally — core can
  still layer on its own semantic rules).
- Fragment spreads and inline fragments should work.

## Non-Goals

- Union types (separate effort).
- Changes to the dagql persistence model.
- Changes to how module SDKs *define* interfaces (the `TypeDef.WithInterface`
  API stays the same; what changes is how the definition gets installed into
  dagql).

## Design

### 1. `InterfaceType` in dagql

Add a new type alongside `Class` (which represents object types):

```go
// dagql/interfaces.go

type Interface struct {
    name        string
    description string
    fields      map[string][]*InterfaceFieldSpec  // same view-keying as Class
    fieldsL     *sync.Mutex
    directives  []*ast.Directive
}

type InterfaceFieldSpec struct {
    FieldSpec           // embedded — same Name, Type, Args, etc.
    MinVersion call.View // view gating, same as object fields
}
```

`Interface` satisfies a new `InterfaceTypeDef` interface (name TBD to avoid
collision with `core.InterfaceTypeDef`):

```go
type InterfaceSchema interface {
    TypeName() string
    FieldSpecs(view call.View) []FieldSpec
}
```

### 2. Server stores interfaces

```go
type Server struct {
    // ...existing...
    interfaces map[string]*Interface  // NEW
}
```

New methods:

```go
func (s *Server) InstallInterface(iface *Interface, directives ...*ast.Directive)
func (s *Server) InterfaceType(name string) (*Interface, bool)
```

### 3. Objects declare interface conformance

Extend `ClassOpts` (or add an `Implements` method on `Class`):

```go
func (c Class[T]) Implements(iface *Interface)
```

This records the relationship. At schema generation time, the object's
`ast.Definition` gets `Interfaces: []string{"Foo", "Bar"}` and the schema's
`PossibleTypes["Foo"]` includes this object's definition.

Calling `Implements` should verify that the object has all the fields the
interface requires (structurally — by name, compatible return type, compatible
args). If not, it panics at install time (programming error, like a bad field
type today).

### 4. Schema generation

In `Server.SchemaForView`:

```go
// existing: objects → ast.Object
sortutil.RangeSorted(s.objects, func(_ string, t ObjectType) {
    def := definition(ast.Object, t, view)
    // NEW: populate def.Interfaces from recorded conformance
    for _, iface := range s.interfacesFor(t.TypeName()) {
        def.Interfaces = append(def.Interfaces, iface.TypeName())
    }
    schema.AddTypes(def)
    schema.AddPossibleType(def.Name, def)
})

// NEW: interfaces → ast.Interface
sortutil.RangeSorted(s.interfaces, func(_ string, iface *Interface) {
    def := iface.Definition(view) // Kind: ast.Interface
    schema.AddTypes(def)
    // add all implementing objects as possible types
    for _, objDef := range s.implementorsOf(iface.name) {
        schema.AddPossibleType(iface.name, objDef)
    }
})
```

### 5. The `Object` interface

Core installs a built-in interface:

```go
// core/schema/query.go or similar

objectIface := dagql.NewInterface("Object", "An object with an identity.")
objectIface.AddField(dagql.InterfaceFieldSpec{
    FieldSpec: dagql.FieldSpec{
        Name: "id",
        Type: dagql.AnyID{},
    },
})
srv.InstallInterface(objectIface)
```

Every class with IDs (the default — `!opts.NoIDs`) automatically implements
`Object`. This happens in `Server.InstallObject`:

```go
func (s *Server) InstallObject(class ObjectType, ...) {
    // ...existing...
    if _, hasID := class.IDType(); hasID {
        if objIface, ok := s.InterfaceType("Object"); ok {
            class.Implements(objIface)
        }
    }
}
```

### 6. Interface values: keep it simple

For **core types** (Container, Directory, etc.), no wrapping is needed. The
object already has all the interface's fields. When a field returns
`Interface`, dagql resolves the concrete object's fields directly — the
interface just constrains which fields are visible in the schema.

For **module types**, the existing `InterfaceAnnotatedValue` wrapping is still
needed because the underlying `ModuleObject` is resolved through the module
runtime. However, the `dagql.InterfaceValue` unwrapping in `toSelectable` stays
— it's fine as-is. What changes is that the schema correctly marks the type as
`ast.Interface` instead of `ast.Object`.

### 7. Eliminate `asFoo` fields

Today `moddeps.go` adds `asFoo` extension fields. With proper interface
support:

- A function returning `Foo` (an interface) can accept any object whose type
  implements `Foo`.
- The `asFoo` field is no longer needed in dagql. The ID itself carries the
  concrete type, and dagql can validate at load time that the concrete type
  implements the interface.

**Migration path:**

1. Keep `asFoo` fields as deprecated for one release cycle.
2. In SDK codegen, stop generating calls to `asFoo` — instead, pass the
   concrete object's ID directly where an interface-typed argument is expected.
3. Remove `asFoo` fields.

The Go SDK specifically needs `asFoo` as a workaround because Go interfaces
can't have covariant return types (`WithFoo() Foo` where the concrete type
returns `*MyImpl` not `Foo`). This stays as an SDK-level concern — the Go
codegen can generate adapter methods that don't correspond to `asFoo` schema
fields.

### 8. Fragment support in `parseASTSelections`

Add handling for `*ast.InlineFragment`:

```go
case *ast.InlineFragment:
    // x.TypeCondition is the type name (e.g. "Container")
    // If the current object's type matches, recurse into its selections
    if x.TypeCondition == "" || self.Name() == x.TypeCondition {
        subsels, err := s.parseASTSelections(ctx, gqlOp, self, x.SelectionSet)
        if err != nil {
            return nil, err
        }
        sels = append(sels, subsels...)
    }
```

For `*ast.FragmentSpread`, add type condition checking:

```go
case *ast.FragmentSpread:
    fragment := gqlOp.Doc.Fragments.ForName(x.Name)
    if fragment.TypeCondition.Name == "" || fragment.TypeCondition.Name == self.Name() {
        // ...existing...
    }
```

### 9. Subtype checking in dagql

Add structural subtype checking that dagql can use:

```go
// dagql/interfaces.go

// Satisfies returns true if the object type has all fields required by the interface.
func (iface *Interface) Satisfies(obj ObjectType, view call.View) bool {
    for _, ifaceField := range iface.FieldSpecs(view) {
        objField, ok := obj.FieldSpec(ifaceField.Name, view)
        if !ok {
            return false
        }
        if !typeCompatible(objField.Type.Type(), ifaceField.Type.Type()) {
            return false
        }
        // check args are contravariant...
    }
    return true
}
```

Core's richer `IsSubtypeOf` (which understands `TypeDef` kinds, optionality,
covariance on returns, contravariance on args) remains in `core/typedef.go`
and continues to be used for module validation. But dagql's `Satisfies` is
used at install time for structural conformance.

### 10. ID handling for interface-typed arguments

When a field argument is interface-typed, its ID scalar should accept any
conforming object's ID. Currently `DynamicID` handles this for module
interfaces. With first-class interfaces:

- The `loadFooFromID` function for an interface validates that the loaded
  object's type implements the interface (via `Satisfies`).
- `AnyID` can be used in argument positions where any `Object` is accepted.
- Typed interface IDs (e.g. `CustomIfaceID`) remain for module-defined
  interfaces.

## Implementation Order

### Phase 1: dagql infrastructure (no behavioral change)

1. Add `Interface` type and `Server.InstallInterface` / `InterfaceType`.
2. Add `Implements` method on `Class`.
3. Update schema generation to emit `ast.Interface` definitions and populate
   `Interfaces` on object definitions.
4. Install `Object { id: ID! }` interface, auto-implement for all idable
   classes.
5. Add inline fragment support in `parseASTSelections`.

### Phase 2: module interfaces use dagql (behavioral change for modules)

6. Change `InterfaceType.Install` (in `core/interface.go`) to use
   `dagql.Interface` instead of creating an object class with
   `InterfaceAnnotatedValue`.
7. Have module object installation call `class.Implements(iface)` for each
   interface the object satisfies.
8. Update `ModDeps.lazilyLoadSchema` to stop adding `asFoo` fields.
9. Deprecate `asFoo` fields (keep them temporarily with a deprecation notice).

### Phase 3: cleanup

10. Remove `InterfaceAnnotatedValue`, `DynamicID` for interfaces, and the
    `dagql.InterfaceValue` unwrapping path.
11. Remove `asFoo` fields entirely.
12. Update SDK codegen to stop generating `asFoo` calls.

## Open Questions

- **Should `Interface` store field funcs?** Probably not — interface fields
  are resolved by the concrete object's field funcs. The interface only needs
  specs (name, type, args) for schema generation and conformance checking.

- **How do interface-typed *return values* work at the dagql level?** When a
  field returns an interface type, the concrete object is returned. The schema
  says the return type is the interface, but the runtime value is the concrete
  object. Introspection's `__typename` returns the concrete type. This is
  standard GraphQL behavior.

- **What about the `DynamicID` scalar for interface arguments?** In Phase 1,
  keep `DynamicID`. In Phase 2, interface arguments could use `AnyID` with
  runtime validation that the loaded object implements the interface. Or keep
  `DynamicID`-like scalars for better error messages. TBD.
