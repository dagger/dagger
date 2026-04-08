package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type Function struct {
	// Name is the standardized name of the function (lowerCamelCase), as used for the resolver in the graphql schema
	Name        string `field:"true" doc:"The name of the function." doNotCache:"simple field selection"`
	Description string `field:"true" doc:"A doc string for the function, if any." doNotCache:"simple field selection"`
	Args        dagql.ObjectResultArray[*FunctionArg]
	ReturnType  dagql.ObjectResult[*TypeDef]
	Deprecated  *string `field:"true" doc:"The reason this function is deprecated, if any."`

	SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]] `field:"true" doc:"The location of this function declaration."`

	// SourceModuleName is set when the function is provided by a module (e.g. a module
	// constructor or auto-alias on the Query root). Empty for core API functions.
	SourceModuleName string `field:"true" doc:"If this function is provided by a module, the name of the module. Unset otherwise."`

	// Below are not in public API
	CachePolicy     FunctionCachePolicy
	CacheTTLSeconds dagql.Nullable[dagql.Int]

	// IsCheck indicates whether this function is a check
	IsCheck bool

	// IsGenerator indicates whether this function is a generator
	IsGenerator bool

	// IsUp indicates whether this function returns a service to be started with `dagger up`
	IsUp bool

	// OriginalName of the parent object
	ParentOriginalName string

	// The original name of the function as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string
}

var _ dagql.PersistedObject = (*Function)(nil)
var _ dagql.PersistedObjectDecoder = (*Function)(nil)
var _ dagql.HasDependencyResults = (*Function)(nil)

func NewFunction(name string, returnType dagql.ObjectResult[*TypeDef]) *Function {
	gqlName := strcase.ToLowerCamel(name)
	if strings.HasPrefix(name, "load") && strings.HasSuffix(name, "FromID") && strings.HasSuffix(gqlName, "FromId") {
		gqlName = strings.TrimSuffix(gqlName, "FromId") + "FromID"
	}
	return &Function{
		Name:         gqlName,
		ReturnType:   returnType,
		OriginalName: name,
	}
}

func (*Function) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Function",
		NonNull:   true,
	}
}

func (*Function) TypeDescription() string {
	return dagql.FormatDescription(
		`Function represents a resolver provided by a Module.`,
		`A function always evaluates against a parent object and is given a set of named arguments.`,
	)
}

func (fn *Function) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if fn == nil {
		return nil, fmt.Errorf("encode persisted function: nil function")
	}
	payload, err := encodePersistedFunction(cache, fn)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*Function) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedFunction
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted function payload: %w", err)
	}
	return decodePersistedFunction(ctx, dag, &persisted)
}

func (fn *Function) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if fn == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 2+len(fn.Args))

	if fn.ReturnType.Self() != nil {
		attached, err := attach(fn.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("attach function return type: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*TypeDef])
		if !ok {
			return nil, fmt.Errorf("attach function return type: unexpected result %T", attached)
		}
		fn.ReturnType = typed
		owned = append(owned, typed)
	}
	if fn.SourceMap.Valid && fn.SourceMap.Value.Self() != nil {
		attached, err := attach(fn.SourceMap.Value)
		if err != nil {
			return nil, fmt.Errorf("attach function source map: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*SourceMap])
		if !ok {
			return nil, fmt.Errorf("attach function source map: unexpected result %T", attached)
		}
		fn.SourceMap = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	for i, arg := range fn.Args {
		if arg.Self() == nil {
			continue
		}
		attached, err := attach(arg)
		if err != nil {
			return nil, fmt.Errorf("attach function arg %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*FunctionArg])
		if !ok {
			return nil, fmt.Errorf("attach function arg %d: unexpected result %T", i, attached)
		}
		fn.Args[i] = typed
		owned = append(owned, typed)
	}

	return owned, nil
}

func (fn Function) Clone() *Function {
	cp := fn
	cp.Args = append(dagql.ObjectResultArray[*FunctionArg](nil), fn.Args...)
	return &cp
}

// Directives returns the GraphQL directives that should be applied to this function.
func (fn *Function) Directives() []*ast.Directive {
	var directives []*ast.Directive
	if fn.IsCheck {
		directives = append(directives, &ast.Directive{
			Name: "check",
		})
	}
	if fn.IsUp {
		directives = append(directives, &ast.Directive{
			Name: "up",
		})
	}
	return directives
}

// FieldSpec converts a Function into a GraphQL field specification for inclusion in a GraphQL schema.
// This method is called during schema generation when building the GraphQL API representation of module functions.
// It transforms the Function's metadata (name, description, arguments, return type) into the dagql.FieldSpec format
// that the GraphQL engine can understand and expose as queryable fields.
//
// The conversion process includes:
// - Converting function arguments to GraphQL input specifications with proper typing
// - Handling default values for arguments by JSON decoding and type validation
// - Adding source map directives for debugging/IDE support
// - Resolving module types through the provided Module context
//
// This is typically called during module loading/registration when the Dagger engine builds
// the complete GraphQL schema that clients will query against.
func (fn *Function) FieldSpec(ctx context.Context, mod Mod) (dagql.FieldSpec, error) {
	spec := dagql.FieldSpec{
		Name:             fn.Name,
		Description:      formatGqlDescription(fn.Description),
		Type:             fn.ReturnType.Self().ToTyped(),
		DeprecatedReason: fn.Deprecated,
	}
	module, err := mod.ResultCallModule(ctx)
	if err != nil {
		return spec, fmt.Errorf("failed to resolve module provenance for function %q: %w", fn.Name, err)
	}
	spec.Module = module
	if fn.SourceMap.Valid && fn.SourceMap.Value.Self() != nil {
		spec.Directives = append(spec.Directives, fn.SourceMap.Value.Self().TypeDirective())
	}
	spec.Directives = append(spec.Directives, fn.Directives()...)
	for _, arg := range fn.Args {
		argSelf := arg.Self()
		modType, ok, err := mod.ModTypeFor(ctx, argSelf.TypeDef.Self(), true)
		if err != nil {
			return spec, fmt.Errorf("failed to get typedef for arg %q: %w", argSelf.Name, err)
		}
		if !ok {
			return spec, fmt.Errorf("failed to get typedef for arg %q", argSelf.Name)
		}

		argTypeDef, err := modType.TypeDef(ctx)
		if err != nil {
			return spec, fmt.Errorf("failed to resolve canonical typedef for arg %q: %w", argSelf.Name, err)
		}

		// Workspace arguments are always optional, regardless of how they're declared in code.
		// They are automatically injected when not explicitly set.
		if argSelf.IsWorkspace() {
			argTypeDef.Self().Optional = true
		}

		input := argTypeDef.Self().ToInput()
		var defaultVal dagql.Input
		if argSelf.DefaultValue != nil {
			var val any
			dec := json.NewDecoder(bytes.NewReader(argSelf.DefaultValue.Bytes()))
			dec.UseNumber()
			if err := dec.Decode(&val); err != nil {
				return spec, fmt.Errorf("failed to decode default value for arg %q: %w", argSelf.Name, err)
			}

			var err error
			defaultVal, err = input.Decoder().DecodeInput(val)
			if err != nil {
				return spec, fmt.Errorf("failed to decode dagql default value for arg %q: %w", argSelf.Name, err)
			}
		}

		argSpec := dagql.InputSpec{
			Name:             argSelf.Name,
			Description:      formatGqlDescription(argSelf.Description),
			Type:             input,
			Default:          defaultVal,
			DeprecatedReason: argSelf.Deprecated,
		}
		if argSelf.SourceMap.Valid && argSelf.SourceMap.Value.Self() != nil {
			argSpec.Directives = append(argSpec.Directives, argSelf.SourceMap.Value.Self().TypeDirective())
		}
		argSpec.Directives = append(argSpec.Directives, argSelf.Directives()...)

		spec.Args.Add(argSpec)
	}

	cachePolicy := fn.CachePolicy
	if cachePolicy == "" {
		cachePolicy = FunctionCachePolicyDefault
	}
	if modInst := mod.ModuleResult(); modInst.Self() != nil {
		cachePolicy = fn.derivedCachePolicy(modInst.Self())
	}
	spec.IsPersistable = true
	switch cachePolicy {
	case FunctionCachePolicyNever:
		spec.DoNotCache = "function explicitly marked as never cache"
		spec.IsPersistable = false

	case FunctionCachePolicyPerSession:
		spec.IsPersistable = false

	case FunctionCachePolicyDefault:
		if fn.CacheTTLSeconds.Valid {
			spec.TTL = fn.CacheTTLSeconds.Value.Int64()
		} else {
			// we still set a max TTL for now as a very primitive form of pruning
			spec.TTL = MaxFunctionCacheTTLSeconds
		}
	}

	return spec, nil
}

func (fn *Function) derivedCachePolicy(mod *Module) FunctionCachePolicy {
	cachePolicy := fn.CachePolicy
	if cachePolicy == "" {
		cachePolicy = FunctionCachePolicyDefault
	}
	if cachePolicy == FunctionCachePolicyDefault && mod.DisableDefaultFunctionCaching {
		// older modules that explicitly disable the new default function caching should
		// fallback to the old caching behavior (per-session)
		cachePolicy = FunctionCachePolicyPerSession
	}

	return cachePolicy
}

func (fn *Function) WithDescription(desc string) *Function {
	fn = fn.Clone()
	fn.Description = strings.TrimSpace(desc)
	return fn
}

func (fn *Function) WithDeprecated(reason *string) *Function {
	fn = fn.Clone()
	fn.Deprecated = reason
	return fn
}

func (fn *Function) WithCheck() *Function {
	fn = fn.Clone()
	fn.IsCheck = true
	return fn
}

func (fn *Function) WithGenerator() *Function {
	fn = fn.Clone()
	fn.IsGenerator = true
	return fn
}

func (fn *Function) WithUp() *Function {
	fn = fn.Clone()
	fn.IsUp = true
	return fn
}

func (fn *Function) WithArg(arg dagql.ObjectResult[*FunctionArg]) *Function {
	fn = fn.Clone()
	for i, existing := range fn.Args {
		existingSelf := existing.Self()
		argSelf := arg.Self()
		if existingSelf == nil || argSelf == nil {
			continue
		}
		switch {
		case existingSelf.OriginalName != "" && argSelf.OriginalName != "" && existingSelf.OriginalName == argSelf.OriginalName:
			fn.Args[i] = arg
			return fn
		case existingSelf.Name == argSelf.Name:
			fn.Args[i] = arg
			return fn
		}
	}
	fn.Args = append(fn.Args, arg)
	return fn
}

func (fn *Function) WithReturnType(returnType dagql.ObjectResult[*TypeDef]) *Function {
	fn = fn.Clone()
	fn.ReturnType = returnType
	return fn
}

func (fn *Function) WithSourceMap(sourceMap dagql.ObjectResult[*SourceMap]) *Function {
	if sourceMap.Self() == nil {
		return fn
	}
	fn = fn.Clone()
	fn.SourceMap = dagql.NonNull(sourceMap)
	return fn
}

func (fn *Function) IsSubtypeOf(otherFn *Function) bool {
	if fn == nil || otherFn == nil {
		return false
	}

	// check return type
	if !fn.ReturnType.Self().IsSubtypeOf(otherFn.ReturnType.Self()) {
		return false
	}

	// check args
	for i, otherFnArgRes := range otherFn.Args {
		/* TODO: with more effort could probably relax and allow:
		* arg names to not match (only types really matter in theory)
		* mismatches in optional (provided defaults exist, etc.)
		* fewer args in interface fn than object fn (as long as the ones that exist match)
		 */

		if i >= len(fn.Args) {
			return false
		}
		fnArg := fn.Args[i].Self()
		otherFnArg := otherFnArgRes.Self()

		if fnArg.Name != otherFnArg.Name {
			return false
		}

		if fnArg.TypeDef.Self().Optional != otherFnArg.TypeDef.Self().Optional {
			return false
		}

		// We want to be contravariant on arg matching types. So if fnArg asks for a Cat, then
		// we can't invoke it with any Animal since it requested a cat specifically.
		// However, if the fnArg asks for an Animal, we can provide a Cat because that's a subtype of Animal.
		// Thus, we check that the otherFnArg is a subtype of the fnArg (inverse of the covariant matching done
		// on function *return* types above).
		if !otherFnArg.TypeDef.Self().IsSubtypeOf(fnArg.TypeDef.Self()) {
			return false
		}
	}

	return true
}

func (fn *Function) LookupArg(nameAnyCase string) (dagql.ObjectResult[*FunctionArg], bool) {
	for _, arg := range fn.Args {
		if strings.EqualFold(arg.Self().Name, nameAnyCase) {
			return arg, true
		}
	}
	return dagql.ObjectResult[*FunctionArg]{}, false
}

func NewFunctionArg(name string, typeDef dagql.ObjectResult[*TypeDef], desc string, defaultValue JSON, defaultPath string, defaultAddress string, ignore []string, deprecated *string) *FunctionArg {
	return &FunctionArg{
		Name:           strcase.ToLowerCamel(name),
		Description:    desc,
		TypeDef:        typeDef,
		DefaultValue:   defaultValue,
		DefaultPath:    defaultPath,
		DefaultAddress: defaultAddress,
		Ignore:         ignore,
		Deprecated:     deprecated,
		OriginalName:   name,
	}
}

type FunctionCachePolicy string

var FunctionCachePolicyEnum = dagql.NewEnum[FunctionCachePolicy]()

var (
	FunctionCachePolicyDefault    = FunctionCachePolicyEnum.Register("Default")
	FunctionCachePolicyPerSession = FunctionCachePolicyEnum.Register("PerSession")
	FunctionCachePolicyNever      = FunctionCachePolicyEnum.Register("Never")
)

func (proto FunctionCachePolicy) Type() *ast.Type {
	return &ast.Type{
		NamedType: "FunctionCachePolicy",
		NonNull:   true,
	}
}

func (proto FunctionCachePolicy) TypeDescription() string {
	return "The behavior configured for function result caching."
}

func (proto FunctionCachePolicy) Decoder() dagql.InputDecoder {
	return FunctionCachePolicyEnum
}

func (proto FunctionCachePolicy) ToLiteral() call.Literal {
	return FunctionCachePolicyEnum.Literal(proto)
}

type FunctionArg struct {
	// Name is the standardized name of the argument (lowerCamelCase), as used for the resolver in the graphql schema
	Name           string                                         `field:"true" doc:"The name of the argument in lowerCamelCase format." doNotCache:"simple field selection"`
	Description    string                                         `field:"true" doc:"A doc string for the argument, if any." doNotCache:"simple field selection"`
	SourceMap      dagql.Nullable[dagql.ObjectResult[*SourceMap]] `field:"true" doc:"The location of this arg declaration."`
	TypeDef        dagql.ObjectResult[*TypeDef]
	DefaultValue   JSON     `field:"true" doc:"A default value to use for this argument when not explicitly set by the caller, if any." doNotCache:"simple field selection"`
	DefaultPath    string   `field:"true" doc:"Only applies to arguments of type File or Directory. If the argument is not set, load it from the given path in the context directory" doNotCache:"simple field selection"`
	DefaultAddress string   `field:"true" doc:"Only applies to arguments of type Container. If the argument is not set, load it from the given address (e.g. alpine:latest)" doNotCache:"simple field selection"`
	Ignore         []string `field:"true" doc:"Only applies to arguments of type Directory. The ignore patterns are applied to the input directory, and matching entries are filtered out, in a cache-efficient manner." doNotCache:"simple field selection"`
	Deprecated     *string  `field:"true" doc:"The reason this function is deprecated, if any."`

	// Below are not in public API

	// The original name of the argument as provided by the SDK that defined it.
	OriginalName string
}

var _ dagql.PersistedObject = (*FunctionArg)(nil)
var _ dagql.PersistedObjectDecoder = (*FunctionArg)(nil)
var _ dagql.HasDependencyResults = (*FunctionArg)(nil)

func (arg *FunctionArg) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if arg == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 2)

	if arg.TypeDef.Self() != nil {
		attached, err := attach(arg.TypeDef)
		if err != nil {
			return nil, fmt.Errorf("attach function arg type def: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*TypeDef])
		if !ok {
			return nil, fmt.Errorf("attach function arg type def: unexpected result %T", attached)
		}
		arg.TypeDef = typed
		owned = append(owned, typed)
	}
	if arg.SourceMap.Valid && arg.SourceMap.Value.Self() != nil {
		attached, err := attach(arg.SourceMap.Value)
		if err != nil {
			return nil, fmt.Errorf("attach function arg source map: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*SourceMap])
		if !ok {
			return nil, fmt.Errorf("attach function arg source map: unexpected result %T", attached)
		}
		arg.SourceMap = dagql.NonNull(typed)
		owned = append(owned, typed)
	}

	return owned, nil
}

func (arg FunctionArg) Clone() *FunctionArg {
	cp := arg
	// NB(vito): don't bother copying DefaultValue, it's already 'any' so it's
	// hard to imagine anything actually mutating it at runtime vs. replacing it
	// wholesale.
	return &cp
}

func (arg *FunctionArg) WithTypeDef(typeDef dagql.ObjectResult[*TypeDef]) *FunctionArg {
	arg = arg.Clone()
	arg.TypeDef = typeDef
	return arg
}

func (arg *FunctionArg) WithSourceMap(sourceMap dagql.ObjectResult[*SourceMap]) *FunctionArg {
	if sourceMap.Self() == nil {
		return arg
	}
	arg = arg.Clone()
	arg.SourceMap = dagql.NonNull(sourceMap)
	return arg
}

func (arg *FunctionArg) WithDefaultValue(defaultValue JSON) *FunctionArg {
	if bytes.Equal(arg.DefaultValue.Bytes(), defaultValue.Bytes()) {
		return arg
	}
	arg = arg.Clone()
	arg.DefaultValue = defaultValue
	return arg
}

func (arg *FunctionArg) WithDefaultPath(defaultPath string) *FunctionArg {
	if arg.DefaultPath == defaultPath {
		return arg
	}
	arg = arg.Clone()
	arg.DefaultPath = defaultPath
	return arg
}

func (arg *FunctionArg) WithDefaultAddress(defaultAddress string) *FunctionArg {
	if arg.DefaultAddress == defaultAddress {
		return arg
	}
	arg = arg.Clone()
	arg.DefaultAddress = defaultAddress
	return arg
}

func (arg *FunctionArg) WithIgnore(ignore []string) *FunctionArg {
	if len(arg.Ignore) == len(ignore) {
		same := true
		for i := range ignore {
			if arg.Ignore[i] != ignore[i] {
				same = false
				break
			}
		}
		if same {
			return arg
		}
	}
	arg = arg.Clone()
	arg.Ignore = append([]string(nil), ignore...)
	return arg
}

// Type returns the GraphQL FunctionArg! type.
func (*FunctionArg) Type() *ast.Type {
	return &ast.Type{
		NamedType: "FunctionArg",
		NonNull:   true,
	}
}

func (*FunctionArg) TypeDescription() string {
	return dagql.FormatDescription(
		`An argument accepted by a function.`,
		`This is a specification for an argument at function definition time, not
		an argument passed at function call time.`)
}

func (arg *FunctionArg) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if arg == nil {
		return nil, fmt.Errorf("encode persisted function arg: nil function arg")
	}
	payload, err := encodePersistedFunctionArg(cache, arg)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*FunctionArg) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedFunctionArg
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted function arg payload: %w", err)
	}
	return decodePersistedFunctionArg(ctx, dag, &persisted)
}

func (arg *FunctionArg) isContextual() bool {
	return arg.DefaultPath != "" || arg.DefaultAddress != ""
}

// IsWorkspace returns true if the argument is of type Workspace.
// Workspace arguments are always optional and automatically injected when not set.
func (arg *FunctionArg) IsWorkspace() bool {
	typeDef := arg.TypeDef.Self()
	return typeDef.Kind == TypeDefKindObject &&
		typeDef.AsObject.Value.Self().Name == "Workspace" &&
		// Functions can't currently accept types from other modules, but be
		// explicit anyway.
		typeDef.AsObject.Value.Self().SourceModuleName == ""
}

func (arg FunctionArg) Directives() []*ast.Directive {
	var directives []*ast.Directive
	if arg.DefaultPath != "" {
		directives = append(directives, &ast.Directive{
			Name: "defaultPath",
			Arguments: ast.ArgumentList{
				{
					Name: "path",
					Value: &ast.Value{
						Kind: ast.StringValue,
						Raw:  arg.DefaultPath,
					},
				},
			},
		})
	}
	if arg.DefaultAddress != "" {
		directives = append(directives, &ast.Directive{
			Name: "defaultAddress",
			Arguments: ast.ArgumentList{
				{
					Name: "address",
					Value: &ast.Value{
						Kind: ast.StringValue,
						Raw:  arg.DefaultAddress,
					},
				},
			},
		})
	}
	if len(arg.Ignore) > 0 {
		var children ast.ChildValueList
		for _, ignore := range arg.Ignore {
			children = append(children, &ast.ChildValue{
				Value: &ast.Value{
					Kind: ast.StringValue,
					Raw:  ignore,
				},
			})
		}
		directives = append(directives, &ast.Directive{
			Name: "ignorePatterns",
			Arguments: ast.ArgumentList{
				&ast.Argument{
					Name: "patterns",
					Value: &ast.Value{
						Kind:     ast.ListValue,
						Children: children,
					},
				},
			},
		})
	}
	return directives
}

type DynamicID struct {
	typeName string
	id       *call.ID
}

var _ dagql.IDable = DynamicID{}

// ID returns the ID of the value.
func (d DynamicID) ID() (*call.ID, error) {
	if d.id == nil {
		return nil, fmt.Errorf("nil dynamic ID")
	}
	return d.id, nil
}

var _ dagql.ScalarType = DynamicID{}

func (d DynamicID) TypeName() string {
	return fmt.Sprintf("%sID", d.typeName)
}

var _ dagql.InputDecoder = DynamicID{}

func (d DynamicID) DecodeInput(val any) (dagql.Input, error) {
	switch x := val.(type) {
	case string:
		var idp call.ID
		if err := idp.Decode(x); err != nil {
			return nil, fmt.Errorf("decode %q ID: %w", d.typeName, err)
		}
		d.id = &idp
		return d, nil
	case *call.ID:
		if x == nil {
			return nil, fmt.Errorf("cannot create %q from nil ID", d.TypeName())
		}
		d.id = x
		return d, nil
	default:
		return nil, fmt.Errorf("expected string for DynamicID, got %T", val)
	}
}

var _ dagql.Input = DynamicID{}

func (d DynamicID) ToLiteral() call.Literal {
	if d.id == nil {
		panic("core.DynamicID.ToLiteral: nil ID")
	}
	if !d.id.IsHandle() {
		panic("core.DynamicID.ToLiteral: recipe-form IDs are not valid inputs")
	}
	enc, err := d.id.Encode()
	if err != nil {
		panic(fmt.Errorf("core.DynamicID.ToLiteral: encode handle ID: %w", err))
	}
	return call.NewLiteralString(enc)
}

func (d DynamicID) Type() *ast.Type {
	return &ast.Type{
		NamedType: d.TypeName(),
		NonNull:   true,
	}
}

func (d DynamicID) Decoder() dagql.InputDecoder {
	return DynamicID{
		typeName: d.typeName,
	}
}

func (d DynamicID) MarshalJSON() ([]byte, error) {
	enc, err := d.id.Encode()
	if err != nil {
		return nil, err
	}
	return json.Marshal(enc)
}

type TypeDef struct {
	Name        string      `field:"true" doc:"The canonical non-optional name of the type." doNotCache:"simple field selection"`
	Kind        TypeDefKind `field:"true" doc:"The kind of type this is (e.g. primitive, list, object)." doNotCache:"simple field selection"`
	Optional    bool        `field:"true" doc:"Whether this type can be set to null. Defaults to false." doNotCache:"simple field selection"`
	AsList      dagql.Nullable[dagql.ObjectResult[*ListTypeDef]]
	AsObject    dagql.Nullable[dagql.ObjectResult[*ObjectTypeDef]]
	AsInterface dagql.Nullable[dagql.ObjectResult[*InterfaceTypeDef]]
	AsInput     dagql.Nullable[dagql.ObjectResult[*InputTypeDef]]
	AsScalar    dagql.Nullable[dagql.ObjectResult[*ScalarTypeDef]]
	AsEnum      dagql.Nullable[dagql.ObjectResult[*EnumTypeDef]]
}

var _ dagql.PersistedObject = (*TypeDef)(nil)
var _ dagql.PersistedObjectDecoder = (*TypeDef)(nil)
var _ dagql.HasDependencyResults = (*TypeDef)(nil)

func (typeDef TypeDef) Clone() *TypeDef {
	cp := typeDef
	return &cp
}

func (typeDef *TypeDef) typeName() string {
	if typeDef == nil {
		return ""
	}
	switch typeDef.Kind {
	case TypeDefKindString:
		return "String"
	case TypeDefKindInteger:
		return "Int"
	case TypeDefKindFloat:
		return "Float"
	case TypeDefKindBoolean:
		return "Boolean"
	case TypeDefKindVoid:
		return "Void"
	case TypeDefKindScalar:
		if typeDef.AsScalar.Valid && typeDef.AsScalar.Value.Self() != nil {
			return typeDef.AsScalar.Value.Self().Name
		}
	case TypeDefKindEnum:
		if typeDef.AsEnum.Valid && typeDef.AsEnum.Value.Self() != nil {
			return typeDef.AsEnum.Value.Self().Name
		}
	case TypeDefKindInput:
		if typeDef.AsInput.Valid && typeDef.AsInput.Value.Self() != nil {
			return typeDef.AsInput.Value.Self().Name
		}
	case TypeDefKindObject:
		if typeDef.AsObject.Valid && typeDef.AsObject.Value.Self() != nil {
			return typeDef.AsObject.Value.Self().Name
		}
	case TypeDefKindInterface:
		if typeDef.AsInterface.Valid && typeDef.AsInterface.Value.Self() != nil {
			return typeDef.AsInterface.Value.Self().Name
		}
	case TypeDefKindList:
		if typeDef.AsList.Valid && typeDef.AsList.Value.Self() != nil {
			return "[" + typeDef.AsList.Value.Self().ElementTypeDef.Self().refTypeName() + "]"
		}
	}
	return ""
}

func (typeDef *TypeDef) refTypeName() string {
	name := typeDef.typeName()
	if name == "" {
		return ""
	}
	if typeDef != nil && typeDef.Optional {
		return name + "?"
	}
	return name
}

func (typeDef *TypeDef) syncName() *TypeDef {
	if typeDef != nil {
		typeDef.Name = typeDef.typeName()
	}
	return typeDef
}

func (*TypeDef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "TypeDef",
		NonNull:   true,
	}
}

func (*TypeDef) TypeDescription() string {
	return "A definition of a parameter or return type in a Module."
}

func (typeDef *TypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if typeDef == nil {
		return nil, fmt.Errorf("encode persisted type def: nil type def")
	}
	payload, err := encodePersistedTypeDef(cache, typeDef)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*TypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted type def payload: %w", err)
	}
	return decodePersistedTypeDef(ctx, dag, &persisted)
}

func (typeDef *TypeDef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if typeDef == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 6)

	if typeDef.AsList.Valid && typeDef.AsList.Value.Self() != nil {
		attached, err := attach(typeDef.AsList.Value)
		if err != nil {
			return nil, fmt.Errorf("attach typedef list: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ListTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach typedef list: unexpected result %T", attached)
		}
		typeDef.AsList = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	if typeDef.AsObject.Valid && typeDef.AsObject.Value.Self() != nil {
		attached, err := attach(typeDef.AsObject.Value)
		if err != nil {
			return nil, fmt.Errorf("attach typedef object: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ObjectTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach typedef object: unexpected result %T", attached)
		}
		typeDef.AsObject = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	if typeDef.AsInterface.Valid && typeDef.AsInterface.Value.Self() != nil {
		attached, err := attach(typeDef.AsInterface.Value)
		if err != nil {
			return nil, fmt.Errorf("attach typedef interface: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*InterfaceTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach typedef interface: unexpected result %T", attached)
		}
		typeDef.AsInterface = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	if typeDef.AsInput.Valid && typeDef.AsInput.Value.Self() != nil {
		attached, err := attach(typeDef.AsInput.Value)
		if err != nil {
			return nil, fmt.Errorf("attach typedef input: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*InputTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach typedef input: unexpected result %T", attached)
		}
		typeDef.AsInput = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	if typeDef.AsScalar.Valid && typeDef.AsScalar.Value.Self() != nil {
		attached, err := attach(typeDef.AsScalar.Value)
		if err != nil {
			return nil, fmt.Errorf("attach typedef scalar: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*ScalarTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach typedef scalar: unexpected result %T", attached)
		}
		typeDef.AsScalar = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	if typeDef.AsEnum.Valid && typeDef.AsEnum.Value.Self() != nil {
		attached, err := attach(typeDef.AsEnum.Value)
		if err != nil {
			return nil, fmt.Errorf("attach typedef enum: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*EnumTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach typedef enum: unexpected result %T", attached)
		}
		typeDef.AsEnum = dagql.NonNull(typed)
		owned = append(owned, typed)
	}

	return owned, nil
}

func (typeDef *TypeDef) ToTyped() dagql.Typed {
	var typed dagql.Typed
	switch typeDef.Kind {
	case TypeDefKindString:
		typed = dagql.String("")
	case TypeDefKindInteger:
		typed = dagql.Int(0)
	case TypeDefKindFloat:
		typed = dagql.Float(0)
	case TypeDefKindBoolean:
		typed = dagql.Boolean(false)
	case TypeDefKindScalar:
		typed = dagql.NewScalar[dagql.String](typeDef.AsScalar.Value.Self().Name, dagql.String(""))
	case TypeDefKindEnum:
		typed = &ModuleEnum{TypeDef: typeDef.AsEnum.Value.Self()}
	case TypeDefKindList:
		typed = dagql.DynamicArrayOutput{Elem: typeDef.AsList.Value.Self().ElementTypeDef.Self().ToTyped()}
	case TypeDefKindObject:
		typed = &ModuleObject{TypeDef: typeDef.AsObject.Value.Self()}
	case TypeDefKindInterface:
		typed = &InterfaceAnnotatedValue{TypeDef: typeDef.AsInterface.Value.Self()}
	case TypeDefKindVoid:
		typed = Void{}
	case TypeDefKindInput:
		typed = typeDef.AsInput.Value.Self().ToInputObjectSpec()
	default:
		panic(fmt.Sprintf("unknown type kind: %s", typeDef.Kind))
	}
	if typeDef.Optional {
		typed = dagql.DynamicNullable{Elem: typed}
	}
	return typed
}

func (typeDef *TypeDef) ToInput() dagql.Input {
	var typed dagql.Input
	switch typeDef.Kind {
	case TypeDefKindString:
		typed = dagql.String("")
	case TypeDefKindInteger:
		typed = dagql.Int(0)
	case TypeDefKindFloat:
		typed = dagql.Float(0)
	case TypeDefKindBoolean:
		typed = dagql.Boolean(false)
	case TypeDefKindScalar:
		typed = dagql.NewScalar[dagql.String](typeDef.AsScalar.Value.Self().Name, dagql.String(""))
	case TypeDefKindEnum:
		typed = &dagql.EnumValueName{Enum: typeDef.AsEnum.Value.Self().Name}
	case TypeDefKindList:
		typed = dagql.DynamicArrayInput{
			Elem: typeDef.AsList.Value.Self().ElementTypeDef.Self().ToInput(),
		}
	case TypeDefKindObject:
		typed = DynamicID{typeName: typeDef.AsObject.Value.Self().Name}
	case TypeDefKindInterface:
		typed = DynamicID{typeName: typeDef.AsInterface.Value.Self().Name}
	case TypeDefKindVoid:
		typed = Void{}
	default:
		panic(fmt.Sprintf("unknown type kind: %s", typeDef.Kind))
	}
	if typeDef.Optional {
		typed = dagql.DynamicOptional{Elem: typed}
	}
	return typed
}

func (typeDef *TypeDef) ToType() *ast.Type {
	return typeDef.ToTyped().Type()
}

func (typeDef *TypeDef) Underlying() *TypeDef {
	switch typeDef.Kind {
	case TypeDefKindList:
		return typeDef.AsList.Value.Self().ElementTypeDef.Self().Underlying()
	default:
		return typeDef
	}
}

func (typeDef *TypeDef) WithKind(kind TypeDefKind) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Kind = kind
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithScalar(scalar dagql.ObjectResult[*ScalarTypeDef]) *TypeDef {
	typeDef = typeDef.WithKind(TypeDefKindScalar)
	typeDef.AsScalar = dagql.NonNull(scalar)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithScalarTypeDef(scalar dagql.ObjectResult[*ScalarTypeDef]) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Kind = TypeDefKindScalar
	typeDef.AsScalar = dagql.NonNull(scalar)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithListOf(list dagql.ObjectResult[*ListTypeDef]) *TypeDef {
	typeDef = typeDef.WithKind(TypeDefKindList)
	typeDef.AsList = dagql.NonNull(list)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithListTypeDef(list dagql.ObjectResult[*ListTypeDef]) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Kind = TypeDefKindList
	typeDef.AsList = dagql.NonNull(list)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithObject(obj dagql.ObjectResult[*ObjectTypeDef]) *TypeDef {
	typeDef = typeDef.WithKind(TypeDefKindObject)
	typeDef.AsObject = dagql.NonNull(obj)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithObjectTypeDef(obj dagql.ObjectResult[*ObjectTypeDef]) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Kind = TypeDefKindObject
	typeDef.AsObject = dagql.NonNull(obj)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithInterface(iface dagql.ObjectResult[*InterfaceTypeDef]) *TypeDef {
	typeDef = typeDef.WithKind(TypeDefKindInterface)
	typeDef.AsInterface = dagql.NonNull(iface)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithInterfaceTypeDef(iface dagql.ObjectResult[*InterfaceTypeDef]) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Kind = TypeDefKindInterface
	typeDef.AsInterface = dagql.NonNull(iface)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithInputTypeDef(input dagql.ObjectResult[*InputTypeDef]) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Kind = TypeDefKindInput
	typeDef.AsInput = dagql.NonNull(input)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithOptional(optional bool) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Optional = optional
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithEnum(enum dagql.ObjectResult[*EnumTypeDef]) *TypeDef {
	typeDef = typeDef.WithKind(TypeDefKindEnum)
	typeDef.AsEnum = dagql.NonNull(enum)
	return typeDef.syncName()
}

func (typeDef *TypeDef) WithEnumTypeDef(enum dagql.ObjectResult[*EnumTypeDef]) *TypeDef {
	typeDef = typeDef.Clone()
	typeDef.Kind = TypeDefKindEnum
	typeDef.AsEnum = dagql.NonNull(enum)
	return typeDef.syncName()
}

func (typeDef *TypeDef) IsSubtypeOf(otherDef *TypeDef) bool {
	if typeDef == nil || otherDef == nil {
		return false
	}

	if typeDef.Optional != otherDef.Optional {
		return false
	}

	switch typeDef.Kind {
	case TypeDefKindString, TypeDefKindInteger, TypeDefKindFloat, TypeDefKindBoolean, TypeDefKindVoid:
		return typeDef.Kind == otherDef.Kind
	case TypeDefKindScalar:
		return typeDef.AsScalar.Value.Self().Name == otherDef.AsScalar.Value.Self().Name
	case TypeDefKindEnum:
		return typeDef.AsEnum.Value.Self().Name == otherDef.AsEnum.Value.Self().Name
	case TypeDefKindList:
		if otherDef.Kind != TypeDefKindList {
			return false
		}
		return typeDef.AsList.Value.Self().ElementTypeDef.Self().IsSubtypeOf(otherDef.AsList.Value.Self().ElementTypeDef.Self())
	case TypeDefKindObject:
		switch otherDef.Kind {
		case TypeDefKindObject:
			// For now, assume that if the objects have the same name, they are the same object. This should be a safe assumption
			// within the context of a single, already-namedspace schema, but not safe if objects are compared across schemas
			return typeDef.AsObject.Value.Self().Name == otherDef.AsObject.Value.Self().Name
		case TypeDefKindInterface:
			return typeDef.AsObject.Value.Self().IsSubtypeOf(otherDef.AsInterface.Value.Self())
		default:
			return false
		}
	case TypeDefKindInterface:
		if otherDef.Kind != TypeDefKindInterface {
			return false
		}
		return typeDef.AsInterface.Value.Self().IsSubtypeOf(otherDef.AsInterface.Value.Self())
	default:
		return false
	}
}

type ObjectTypeDef struct {
	// Name is the standardized name of the object (CamelCase), as used for the object in the graphql schema
	Name        string                                         `field:"true" doc:"The name of the object." doNotCache:"simple field selection"`
	Description string                                         `field:"true" doc:"The doc string for the object, if any." doNotCache:"simple field selection"`
	SourceMap   dagql.Nullable[dagql.ObjectResult[*SourceMap]] `field:"true" doc:"The location of this object declaration."`
	Fields      dagql.ObjectResultArray[*FieldTypeDef]
	Functions   dagql.ObjectResultArray[*Function]
	Constructor dagql.Nullable[dagql.ObjectResult[*Function]]
	Deprecated  *string `field:"true" doc:"The reason this enum member is deprecated, if any."`

	// SourceModuleName is currently only set when returning the TypeDef from the Objects field on Module
	SourceModuleName string `field:"true" doc:"If this ObjectTypeDef is associated with a Module, the name of the module. Unset otherwise." doNotCache:"simple field selection"`

	// Below are not in public API

	// The original name of the object as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string

	// IsMainObject is true when this object is the primary (entry-point)
	// object of its source module, as determined by Module.MainObject().
	// Set by Module.TypeDefs() so downstream consumers don't need
	// name-matching heuristics.
	IsMainObject bool
}

func (obj ObjectTypeDef) functions() iter.Seq[*Function] {
	return func(yield func(*Function) bool) {
		if obj.Constructor.Valid && obj.Constructor.Value.Self() != nil {
			if !yield(obj.Constructor.Value.Self()) {
				return
			}
		}
		for _, objFn := range obj.Functions {
			if !yield(objFn.Self()) {
				return
			}
		}
	}
}

func (*ObjectTypeDef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ObjectTypeDef",
		NonNull:   true,
	}
}

func (*ObjectTypeDef) TypeDescription() string {
	return "A definition of a custom object defined in a Module."
}

var _ dagql.HasDependencyResults = (*ObjectTypeDef)(nil)

func (obj *ObjectTypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if obj == nil {
		return nil, fmt.Errorf("encode persisted object type def: nil object type def")
	}
	payload, err := encodePersistedObjectTypeDef(cache, obj)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*ObjectTypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedObjectTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted object type def payload: %w", err)
	}
	return decodePersistedObjectTypeDef(ctx, dag, &persisted)
}

func (obj *ObjectTypeDef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if obj == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 2+len(obj.Fields)+len(obj.Functions))

	if obj.SourceMap.Valid && obj.SourceMap.Value.Self() != nil {
		attached, err := attach(obj.SourceMap.Value)
		if err != nil {
			return nil, fmt.Errorf("attach object typedef source map: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*SourceMap])
		if !ok {
			return nil, fmt.Errorf("attach object typedef source map: unexpected result %T", attached)
		}
		obj.SourceMap = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	for i, field := range obj.Fields {
		if field.Self() == nil {
			continue
		}
		attached, err := attach(field)
		if err != nil {
			return nil, fmt.Errorf("attach object typedef field %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*FieldTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach object typedef field %d: unexpected result %T", i, attached)
		}
		obj.Fields[i] = typed
		owned = append(owned, typed)
	}
	for i, fn := range obj.Functions {
		if fn.Self() == nil {
			continue
		}
		attached, err := attach(fn)
		if err != nil {
			return nil, fmt.Errorf("attach object typedef function %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Function])
		if !ok {
			return nil, fmt.Errorf("attach object typedef function %d: unexpected result %T", i, attached)
		}
		obj.Functions[i] = typed
		owned = append(owned, typed)
	}
	if obj.Constructor.Valid && obj.Constructor.Value.Self() != nil {
		attached, err := attach(obj.Constructor.Value)
		if err != nil {
			return nil, fmt.Errorf("attach object typedef constructor: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Function])
		if !ok {
			return nil, fmt.Errorf("attach object typedef constructor: unexpected result %T", attached)
		}
		obj.Constructor = dagql.NonNull(typed)
		owned = append(owned, typed)
	}

	return owned, nil
}

func NewObjectTypeDef(name, description string, deprecated *string) *ObjectTypeDef {
	return &ObjectTypeDef{
		Name:         strcase.ToCamel(name),
		OriginalName: name,
		Description:  description,
		Deprecated:   deprecated,
	}
}

func (obj ObjectTypeDef) Clone() *ObjectTypeDef {
	cp := obj
	cp.Fields = append(dagql.ObjectResultArray[*FieldTypeDef](nil), obj.Fields...)
	cp.Functions = append(dagql.ObjectResultArray[*Function](nil), obj.Functions...)
	return &cp
}

func (obj *ObjectTypeDef) WithSourceMap(sourceMap dagql.ObjectResult[*SourceMap]) *ObjectTypeDef {
	if sourceMap.Self() == nil {
		return obj
	}
	obj = obj.Clone()
	obj.SourceMap = dagql.NonNull(sourceMap)
	return obj
}

func (obj *ObjectTypeDef) WithName(name string) *ObjectTypeDef {
	obj = obj.Clone()
	obj.Name = strcase.ToCamel(name)
	return obj
}

func (obj *ObjectTypeDef) WithSourceModuleName(sourceModuleName string) *ObjectTypeDef {
	obj = obj.Clone()
	obj.SourceModuleName = sourceModuleName
	return obj
}

func (obj *ObjectTypeDef) FieldByName(name string) (*FieldTypeDef, bool) {
	for _, field := range obj.Fields {
		if field.Self().Name == name {
			return field.Self(), true
		}
	}
	return nil, false
}

func (obj *ObjectTypeDef) FieldByOriginalName(name string) (*FieldTypeDef, bool) {
	for _, field := range obj.Fields {
		if field.Self().OriginalName == name {
			return field.Self(), true
		}
	}
	return nil, false
}

func (obj *ObjectTypeDef) FunctionByName(name string) (*Function, bool) {
	for _, fn := range obj.Functions {
		if fn.Self().Name == gqlFieldName(name) {
			return fn.Self(), true
		}
	}
	return nil, false
}

func (obj *ObjectTypeDef) IsSubtypeOf(iface *InterfaceTypeDef) bool {
	if obj == nil || iface == nil {
		return false
	}

	objFnByName := make(map[string]*Function)
	for _, fn := range obj.Functions {
		objFnByName[fn.Self().Name] = fn.Self()
	}
	objFieldByName := make(map[string]*FieldTypeDef)
	for _, field := range obj.Fields {
		objFieldByName[field.Self().Name] = field.Self()
	}

	for _, ifaceFnRes := range iface.Functions {
		ifaceFn := ifaceFnRes.Self()
		objFn, objFnExists := objFnByName[ifaceFn.Name]
		objField, objFieldExists := objFieldByName[ifaceFn.Name]

		if !objFnExists && !objFieldExists {
			return false
		}

		if objFieldExists {
			// check return type of field
			return objField.TypeDef.Self().IsSubtypeOf(ifaceFn.ReturnType.Self())
		}

		// otherwise there can only be a match on the objFn
		if ok := objFn.IsSubtypeOf(ifaceFn); !ok {
			return false
		}
	}

	return true
}

func (obj *ObjectTypeDef) WithField(field dagql.ObjectResult[*FieldTypeDef]) *ObjectTypeDef {
	obj = obj.Clone()
	for i, existing := range obj.Fields {
		existingSelf := existing.Self()
		fieldSelf := field.Self()
		if existingSelf == nil || fieldSelf == nil {
			continue
		}
		switch {
		case existingSelf.OriginalName != "" && fieldSelf.OriginalName != "" && existingSelf.OriginalName == fieldSelf.OriginalName:
			obj.Fields[i] = field
			return obj
		case existingSelf.Name == fieldSelf.Name:
			obj.Fields[i] = field
			return obj
		}
	}
	obj.Fields = append(obj.Fields, field)
	return obj
}

func (obj *ObjectTypeDef) WithFunction(fn dagql.ObjectResult[*Function]) *ObjectTypeDef {
	obj = obj.Clone()
	for i, existing := range obj.Functions {
		existingSelf := existing.Self()
		fnSelf := fn.Self()
		if existingSelf == nil || fnSelf == nil {
			continue
		}
		switch {
		case existingSelf.OriginalName != "" && fnSelf.OriginalName != "" && existingSelf.OriginalName == fnSelf.OriginalName:
			obj.Functions[i] = fn
			return obj
		case existingSelf.Name == fnSelf.Name:
			obj.Functions[i] = fn
			return obj
		}
	}
	obj.Functions = append(obj.Functions, fn)
	return obj
}

func (obj *ObjectTypeDef) WithConstructor(fn dagql.ObjectResult[*Function]) *ObjectTypeDef {
	obj = obj.Clone()
	obj.Constructor = dagql.NonNull(fn)
	return obj
}

type FieldTypeDef struct {
	Name        string `field:"true" doc:"The name of the field in lowerCamelCase format." doNotCache:"simple field selection"`
	Description string `field:"true" doc:"A doc string for the field, if any." doNotCache:"simple field selection"`
	TypeDef     dagql.ObjectResult[*TypeDef]

	SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]] `field:"true" doc:"The location of this field declaration."`

	Deprecated *string `field:"true" doc:"The reason this enum member is deprecated, if any."`

	// Below are not in public API

	// The original name of the object as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string
}

func NewFieldTypeDef(name string, typeDef dagql.ObjectResult[*TypeDef], description string, deprecated *string) *FieldTypeDef {
	return &FieldTypeDef{
		Name:         strcase.ToLowerCamel(name),
		Description:  description,
		TypeDef:      typeDef,
		Deprecated:   deprecated,
		OriginalName: name,
	}
}

func (*FieldTypeDef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "FieldTypeDef",
		NonNull:   true,
	}
}

func (*FieldTypeDef) TypeDescription() string {
	return dagql.FormatDescription(
		`A definition of a field on a custom object defined in a Module.`,
		`A field on an object has a static value, as opposed to a function on an
		object whose value is computed by invoking code (and can accept
		arguments).`)
}

var _ dagql.HasDependencyResults = (*FieldTypeDef)(nil)

func (field *FieldTypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if field == nil {
		return nil, fmt.Errorf("encode persisted field type def: nil field type def")
	}
	payload, err := encodePersistedFieldTypeDef(cache, field)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*FieldTypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedFieldTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted field type def payload: %w", err)
	}
	return decodePersistedFieldTypeDef(ctx, dag, &persisted)
}

func (field *FieldTypeDef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if field == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 2)

	if field.TypeDef.Self() != nil {
		attached, err := attach(field.TypeDef)
		if err != nil {
			return nil, fmt.Errorf("attach field typedef type: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*TypeDef])
		if !ok {
			return nil, fmt.Errorf("attach field typedef type: unexpected result %T", attached)
		}
		field.TypeDef = typed
		owned = append(owned, typed)
	}
	if field.SourceMap.Valid && field.SourceMap.Value.Self() != nil {
		attached, err := attach(field.SourceMap.Value)
		if err != nil {
			return nil, fmt.Errorf("attach field typedef source map: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*SourceMap])
		if !ok {
			return nil, fmt.Errorf("attach field typedef source map: unexpected result %T", attached)
		}
		field.SourceMap = dagql.NonNull(typed)
		owned = append(owned, typed)
	}

	return owned, nil
}

func (typeDef FieldTypeDef) Clone() *FieldTypeDef {
	cp := typeDef
	return &cp
}

func (field *FieldTypeDef) WithTypeDef(typeDef dagql.ObjectResult[*TypeDef]) *FieldTypeDef {
	field = field.Clone()
	field.TypeDef = typeDef
	return field
}

func (field *FieldTypeDef) WithSourceMap(sourceMap dagql.ObjectResult[*SourceMap]) *FieldTypeDef {
	if sourceMap.Self() == nil {
		return field
	}
	field = field.Clone()
	field.SourceMap = dagql.NonNull(sourceMap)
	return field
}

type InterfaceTypeDef struct {
	// Name is the standardized name of the interface (CamelCase), as used for the interface in the graphql schema
	Name        string                                         `field:"true" doc:"The name of the interface." doNotCache:"simple field selection"`
	Description string                                         `field:"true" doc:"The doc string for the interface, if any." doNotCache:"simple field selection"`
	SourceMap   dagql.Nullable[dagql.ObjectResult[*SourceMap]] `field:"true" doc:"The location of this interface declaration."`
	Functions   dagql.ObjectResultArray[*Function]
	// SourceModuleName is currently only set when returning the TypeDef from the Objects field on Module
	SourceModuleName string `field:"true" doc:"If this InterfaceTypeDef is associated with a Module, the name of the module. Unset otherwise." doNotCache:"simple field selection"`

	// Below are not in public API

	// The original name of the interface as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string
}

func NewInterfaceTypeDef(name, description string) *InterfaceTypeDef {
	return &InterfaceTypeDef{
		Name:         strcase.ToCamel(name),
		OriginalName: name,
		Description:  description,
	}
}

func (*InterfaceTypeDef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "InterfaceTypeDef",
		NonNull:   true,
	}
}

func (*InterfaceTypeDef) TypeDescription() string {
	return "A definition of a custom interface defined in a Module."
}

var _ dagql.HasDependencyResults = (*InterfaceTypeDef)(nil)

func (iface *InterfaceTypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if iface == nil {
		return nil, fmt.Errorf("encode persisted interface type def: nil interface type def")
	}
	payload, err := encodePersistedInterfaceTypeDef(cache, iface)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*InterfaceTypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedInterfaceTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted interface type def payload: %w", err)
	}
	return decodePersistedInterfaceTypeDef(ctx, dag, &persisted)
}

func (iface *InterfaceTypeDef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if iface == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 1+len(iface.Functions))

	if iface.SourceMap.Valid && iface.SourceMap.Value.Self() != nil {
		attached, err := attach(iface.SourceMap.Value)
		if err != nil {
			return nil, fmt.Errorf("attach interface typedef source map: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*SourceMap])
		if !ok {
			return nil, fmt.Errorf("attach interface typedef source map: unexpected result %T", attached)
		}
		iface.SourceMap = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	for i, fn := range iface.Functions {
		if fn.Self() == nil {
			continue
		}
		attached, err := attach(fn)
		if err != nil {
			return nil, fmt.Errorf("attach interface typedef function %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Function])
		if !ok {
			return nil, fmt.Errorf("attach interface typedef function %d: unexpected result %T", i, attached)
		}
		iface.Functions[i] = typed
		owned = append(owned, typed)
	}

	return owned, nil
}

func (iface InterfaceTypeDef) Clone() *InterfaceTypeDef {
	cp := iface
	cp.Functions = append(dagql.ObjectResultArray[*Function](nil), iface.Functions...)
	return &cp
}

func (iface *InterfaceTypeDef) WithSourceMap(sourceMap dagql.ObjectResult[*SourceMap]) *InterfaceTypeDef {
	if sourceMap.Self() == nil {
		return iface
	}
	iface = iface.Clone()
	iface.SourceMap = dagql.NonNull(sourceMap)
	return iface
}

func (iface *InterfaceTypeDef) WithName(name string) *InterfaceTypeDef {
	iface = iface.Clone()
	iface.Name = strcase.ToCamel(name)
	return iface
}

func (iface *InterfaceTypeDef) WithSourceModuleName(sourceModuleName string) *InterfaceTypeDef {
	iface = iface.Clone()
	iface.SourceModuleName = sourceModuleName
	return iface
}

func (iface *InterfaceTypeDef) IsSubtypeOf(otherIface *InterfaceTypeDef) bool {
	if iface == nil || otherIface == nil {
		return false
	}

	ifaceFnByName := make(map[string]*Function)
	for _, fn := range iface.Functions {
		ifaceFnByName[fn.Self().Name] = fn.Self()
	}

	for _, otherIfaceFnRes := range otherIface.Functions {
		otherIfaceFn := otherIfaceFnRes.Self()
		ifaceFn, ok := ifaceFnByName[otherIfaceFn.Name]
		if !ok {
			return false
		}

		if ok := ifaceFn.IsSubtypeOf(otherIfaceFn); !ok {
			return false
		}
	}

	return true
}

func (iface *InterfaceTypeDef) WithFunction(fn dagql.ObjectResult[*Function]) *InterfaceTypeDef {
	iface = iface.Clone()
	for i, existing := range iface.Functions {
		existingSelf := existing.Self()
		fnSelf := fn.Self()
		if existingSelf == nil || fnSelf == nil {
			continue
		}
		switch {
		case existingSelf.OriginalName != "" && fnSelf.OriginalName != "" && existingSelf.OriginalName == fnSelf.OriginalName:
			iface.Functions[i] = fn
			return iface
		case existingSelf.Name == fnSelf.Name:
			iface.Functions[i] = fn
			return iface
		}
	}
	iface.Functions = append(iface.Functions, fn)
	return iface
}

type ScalarTypeDef struct {
	Name        string `field:"true" doc:"The name of the scalar." doNotCache:"simple field selection"`
	Description string `field:"true" doc:"A doc string for the scalar, if any." doNotCache:"simple field selection"`

	OriginalName string

	// SourceModuleName is currently only set when returning the TypeDef from the Scalars field on Module
	SourceModuleName string `field:"true" doc:"If this ScalarTypeDef is associated with a Module, the name of the module. Unset otherwise." doNotCache:"simple field selection"`
}

func NewScalarTypeDef(name, description string) *ScalarTypeDef {
	return &ScalarTypeDef{
		Name:         strcase.ToCamel(name),
		OriginalName: name,
		Description:  description,
	}
}

func (*ScalarTypeDef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ScalarTypeDef",
		NonNull:   true,
	}
}

func (typeDef *ScalarTypeDef) TypeDescription() string {
	return "A definition of a custom scalar defined in a Module."
}

func (typeDef *ScalarTypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	if typeDef == nil {
		return nil, fmt.Errorf("encode persisted scalar type def: nil scalar type def")
	}
	return json.Marshal(encodePersistedScalarTypeDef(typeDef))
}

func (*ScalarTypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var persisted persistedScalarTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted scalar type def payload: %w", err)
	}
	return decodePersistedScalarTypeDef(&persisted), nil
}

func (typeDef ScalarTypeDef) Clone() *ScalarTypeDef {
	return &typeDef
}

type ListTypeDef struct {
	ElementTypeDef dagql.ObjectResult[*TypeDef]
}

func (*ListTypeDef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ListTypeDef",
		NonNull:   true,
	}
}

func (*ListTypeDef) TypeDescription() string {
	return "A definition of a list type in a Module."
}

var _ dagql.HasDependencyResults = (*ListTypeDef)(nil)

func (typeDef *ListTypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if typeDef == nil {
		return nil, fmt.Errorf("encode persisted list type def: nil list type def")
	}
	payload, err := encodePersistedListTypeDef(cache, typeDef)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*ListTypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedListTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted list type def payload: %w", err)
	}
	return decodePersistedListTypeDef(ctx, dag, &persisted)
}

func (typeDef *ListTypeDef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if typeDef == nil || typeDef.ElementTypeDef.Self() == nil {
		return nil, nil
	}

	attached, err := attach(typeDef.ElementTypeDef)
	if err != nil {
		return nil, fmt.Errorf("attach list typedef element type: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*TypeDef])
	if !ok {
		return nil, fmt.Errorf("attach list typedef element type: unexpected result %T", attached)
	}
	typeDef.ElementTypeDef = typed
	return []dagql.AnyResult{typed}, nil
}

func (typeDef ListTypeDef) Clone() *ListTypeDef {
	return &typeDef
}

func (typeDef *ListTypeDef) WithElementTypeDef(elementTypeDef dagql.ObjectResult[*TypeDef]) *ListTypeDef {
	typeDef = typeDef.Clone()
	typeDef.ElementTypeDef = elementTypeDef
	return typeDef
}

type InputTypeDef struct {
	Name   string `field:"true" doc:"The name of the input object." doNotCache:"simple field selection"`
	Fields dagql.ObjectResultArray[*FieldTypeDef]
}

func (*InputTypeDef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "InputTypeDef",
		NonNull:   true,
	}
}

func (*InputTypeDef) TypeDescription() string {
	return `A graphql input type, which is essentially just a group of named args.
This is currently only used to represent pre-existing usage of graphql input types
in the core API. It is not used by user modules and shouldn't ever be as user
module accept input objects via their id rather than graphql input types.`
}

var _ dagql.HasDependencyResults = (*InputTypeDef)(nil)

func (typeDef *InputTypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if typeDef == nil {
		return nil, fmt.Errorf("encode persisted input type def: nil input type def")
	}
	payload, err := encodePersistedInputTypeDef(cache, typeDef)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*InputTypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedInputTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted input type def payload: %w", err)
	}
	return decodePersistedInputTypeDef(ctx, dag, &persisted)
}

func (typeDef *InputTypeDef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if typeDef == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, len(typeDef.Fields))
	for i, field := range typeDef.Fields {
		if field.Self() == nil {
			continue
		}
		attached, err := attach(field)
		if err != nil {
			return nil, fmt.Errorf("attach input typedef field %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*FieldTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach input typedef field %d: unexpected result %T", i, attached)
		}
		typeDef.Fields[i] = typed
		owned = append(owned, typed)
	}
	return owned, nil
}

func (typeDef InputTypeDef) Clone() *InputTypeDef {
	cp := typeDef
	cp.Fields = append(dagql.ObjectResultArray[*FieldTypeDef](nil), typeDef.Fields...)
	return &cp
}

func (typeDef *InputTypeDef) ToInputObjectSpec() dagql.InputObjectSpec {
	spec := dagql.InputObjectSpec{
		Name: typeDef.Name,
	}
	for _, field := range typeDef.Fields {
		fieldSelf := field.Self()
		spec.Fields.Add(dagql.InputSpec{
			Name:        fieldSelf.Name,
			Description: fieldSelf.Description,
			Type:        fieldSelf.TypeDef.Self().ToInput(),
		})
	}
	return spec
}

func (typeDef *InputTypeDef) WithField(field dagql.ObjectResult[*FieldTypeDef]) *InputTypeDef {
	typeDef = typeDef.Clone()
	typeDef.Fields = append(typeDef.Fields, field)
	return typeDef
}

type EnumTypeDef struct {
	// Name is the standardized name of the enum (CamelCase), as used for the enum in the graphql schema
	Name        string `field:"true" doc:"The name of the enum." doNotCache:"simple field selection"`
	Description string `field:"true" doc:"A doc string for the enum, if any." doNotCache:"simple field selection"`
	Members     dagql.ObjectResultArray[*EnumMemberTypeDef]
	SourceMap   dagql.Nullable[dagql.ObjectResult[*SourceMap]] `field:"true" doc:"The location of this enum declaration."`

	// SourceModuleName is currently only set when returning the TypeDef from the Enum field on Module
	SourceModuleName string `field:"true" doc:"If this EnumTypeDef is associated with a Module, the name of the module. Unset otherwise." doNotCache:"simple field selection"`

	// Below are not in public API

	// The original name of the enum as provided by the SDK that defined it, used
	// when invoking the SDK so it doesn't need to think as hard about case conversions
	OriginalName string
}

func (*EnumTypeDef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EnumTypeDef",
		NonNull:   true,
	}
}

func (*EnumTypeDef) TypeDescription() string {
	return "A definition of a custom enum defined in a Module."
}

var _ dagql.HasDependencyResults = (*EnumTypeDef)(nil)

func (enum *EnumTypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if enum == nil {
		return nil, fmt.Errorf("encode persisted enum type def: nil enum type def")
	}
	payload, err := encodePersistedEnumTypeDef(cache, enum)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*EnumTypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedEnumTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted enum type def payload: %w", err)
	}
	return decodePersistedEnumTypeDef(ctx, dag, &persisted)
}

func (enum *EnumTypeDef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if enum == nil {
		return nil, nil
	}

	owned := make([]dagql.AnyResult, 0, 1+len(enum.Members))

	if enum.SourceMap.Valid && enum.SourceMap.Value.Self() != nil {
		attached, err := attach(enum.SourceMap.Value)
		if err != nil {
			return nil, fmt.Errorf("attach enum typedef source map: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*SourceMap])
		if !ok {
			return nil, fmt.Errorf("attach enum typedef source map: unexpected result %T", attached)
		}
		enum.SourceMap = dagql.NonNull(typed)
		owned = append(owned, typed)
	}
	for i, member := range enum.Members {
		if member.Self() == nil {
			continue
		}
		attached, err := attach(member)
		if err != nil {
			return nil, fmt.Errorf("attach enum typedef member %d: %w", i, err)
		}
		typed, ok := attached.(dagql.ObjectResult[*EnumMemberTypeDef])
		if !ok {
			return nil, fmt.Errorf("attach enum typedef member %d: unexpected result %T", i, attached)
		}
		enum.Members[i] = typed
		owned = append(owned, typed)
	}

	return owned, nil
}

func NewEnumTypeDef(name, description string, sourceMap dagql.ObjectResult[*SourceMap]) *EnumTypeDef {
	typedef := &EnumTypeDef{
		Name:         strcase.ToCamel(name),
		OriginalName: name,
		Description:  description,
	}
	if sourceMap.Self() != nil {
		typedef.SourceMap = dagql.NonNull(sourceMap)
	}
	return typedef
}

func (enum EnumTypeDef) Clone() *EnumTypeDef {
	cp := enum
	cp.Members = append(dagql.ObjectResultArray[*EnumMemberTypeDef](nil), enum.Members...)
	return &cp
}

func (enum *EnumTypeDef) WithName(name string) *EnumTypeDef {
	enum = enum.Clone()
	enum.Name = strcase.ToCamel(name)
	return enum
}

func (enum *EnumTypeDef) WithSourceMap(sourceMap dagql.ObjectResult[*SourceMap]) *EnumTypeDef {
	if sourceMap.Self() == nil {
		return enum
	}
	enum = enum.Clone()
	enum.SourceMap = dagql.NonNull(sourceMap)
	return enum
}

func (enum *EnumTypeDef) WithSourceModuleName(sourceModuleName string) *EnumTypeDef {
	enum = enum.Clone()
	enum.SourceModuleName = sourceModuleName
	return enum
}

func (enum *EnumTypeDef) WithMember(member dagql.ObjectResult[*EnumMemberTypeDef]) (*EnumTypeDef, error) {
	enum = enum.Clone()
	memberSelf := member.Self()
	if memberSelf == nil {
		return enum, nil
	}

	replacementIdx := -1
	for i, existing := range enum.Members {
		existingSelf := existing.Self()
		if existingSelf == nil {
			continue
		}
		switch {
		case existingSelf.OriginalName != "" && memberSelf.OriginalName != "" && existingSelf.OriginalName == memberSelf.OriginalName:
			replacementIdx = i
		case existingSelf.Name == memberSelf.Name:
			replacementIdx = i
		}
		if replacementIdx != -1 {
			break
		}
	}

	pattern := `^[a-zA-Z_][a-zA-Z0-9_]*$`
	if !regexp.MustCompile(pattern).MatchString(memberSelf.Name) {
		return nil, fmt.Errorf("enum name %q is not valid (only letters, digits and underscores are allowed)", memberSelf.Name)
	}
	for i, existing := range enum.Members {
		if i == replacementIdx {
			continue
		}
		existingSelf := existing.Self()
		if existingSelf == nil {
			continue
		}
		if existingSelf.Name == memberSelf.Name {
			return nil, fmt.Errorf("enum %q is already defined", memberSelf.Name)
		}
		if memberSelf.Value != "" && existingSelf.Value == memberSelf.Value {
			return nil, fmt.Errorf("enum %q is already defined with value %q", existingSelf.Name, memberSelf.Value)
		}
	}
	if replacementIdx != -1 {
		enum.Members[replacementIdx] = member
		return enum, nil
	}
	enum.Members = append(enum.Members, member)
	return enum, nil
}

type EnumMemberTypeDef struct {
	Name        string                                         `field:"true" doc:"The name of the enum member." doNotCache:"simple field selection"`
	Value       string                                         `field:"true" doc:"The value of the enum member" doNotCache:"simple field selection"`
	Description string                                         `field:"true" doc:"A doc string for the enum member, if any." doNotCache:"simple field selection"`
	SourceMap   dagql.Nullable[dagql.ObjectResult[*SourceMap]] `field:"true" doc:"The location of this enum member declaration."`
	Deprecated  *string                                        `field:"true" doc:"The reason this enum member is deprecated, if any."`

	OriginalName string
}

func (*EnumMemberTypeDef) Type() *ast.Type {
	return &ast.Type{
		// FIXME: currently preserved as a legacy type (since we don't support
		// renaming types)
		NamedType: "EnumValueTypeDef",
		NonNull:   true,
	}
}

func (*EnumMemberTypeDef) TypeDescription() string {
	return "A definition of a value in a custom enum defined in a Module."
}

var _ dagql.HasDependencyResults = (*EnumMemberTypeDef)(nil)

func (member *EnumMemberTypeDef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if member == nil {
		return nil, fmt.Errorf("encode persisted enum member type def: nil enum member type def")
	}
	payload, err := encodePersistedEnumMemberTypeDef(cache, member)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func (*EnumMemberTypeDef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedEnumMemberTypeDef
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted enum member type def payload: %w", err)
	}
	return decodePersistedEnumMemberTypeDef(ctx, dag, &persisted)
}

func (member *EnumMemberTypeDef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if member == nil || !member.SourceMap.Valid || member.SourceMap.Value.Self() == nil {
		return nil, nil
	}

	attached, err := attach(member.SourceMap.Value)
	if err != nil {
		return nil, fmt.Errorf("attach enum member source map: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*SourceMap])
	if !ok {
		return nil, fmt.Errorf("attach enum member source map: unexpected result %T", attached)
	}
	member.SourceMap = dagql.NonNull(typed)
	return []dagql.AnyResult{typed}, nil
}

func NewEnumMemberTypeDef(name, value, description string, deprecated *string, sourceMap dagql.ObjectResult[*SourceMap]) *EnumMemberTypeDef {
	typedef := &EnumMemberTypeDef{
		OriginalName: name,
		Name:         strcase.ToScreamingSnake(name),
		Value:        value,
		Description:  description,
		Deprecated:   deprecated,
	}
	if sourceMap.Self() != nil {
		typedef.SourceMap = dagql.NonNull(sourceMap)
	}
	return typedef
}

func NewEnumValueTypeDef(name, value, description string, deprecated *string, sourceMap dagql.ObjectResult[*SourceMap]) *EnumMemberTypeDef {
	typedef := &EnumMemberTypeDef{
		OriginalName: name,
		Name:         value,
		Value:        value,
		Description:  description,
		Deprecated:   deprecated,
	}
	if sourceMap.Self() != nil {
		typedef.SourceMap = dagql.NonNull(sourceMap)
	}
	return typedef
}

func (enumValue EnumMemberTypeDef) Clone() *EnumMemberTypeDef {
	return &enumValue
}

func (enumValue *EnumMemberTypeDef) WithName(name string) *EnumMemberTypeDef {
	enumValue = enumValue.Clone()
	enumValue.Name = strcase.ToScreamingSnake(name)
	return enumValue
}

func (enumValue *EnumMemberTypeDef) WithSourceMap(sourceMap dagql.ObjectResult[*SourceMap]) *EnumMemberTypeDef {
	if sourceMap.Self() == nil {
		return enumValue
	}
	enumValue = enumValue.Clone()
	enumValue.SourceMap = dagql.NonNull(sourceMap)
	return enumValue
}

func (enumValue *EnumMemberTypeDef) EnumValueDirectives() []*ast.Directive {
	directives := []*ast.Directive{}

	if enumValue.Deprecated != nil {
		dir := &ast.Directive{Name: "deprecated"}
		if reason := *enumValue.Deprecated; reason != "" {
			dir.Arguments = ast.ArgumentList{
				&ast.Argument{
					Name: "reason",
					Value: &ast.Value{
						Kind: ast.StringValue,
						Raw:  reason,
					},
				},
			}
		}
		directives = append(directives, dir)
	}

	if enumValue.Value != "" && enumValue.Value != enumValue.Name {
		directives = append(directives, &ast.Directive{
			Name: "enumValue",
			Arguments: ast.ArgumentList{
				&ast.Argument{
					Name: "value",
					Value: &ast.Value{
						Kind: ast.StringValue,
						Raw:  enumValue.Value,
					},
				},
			},
		})
	}

	return directives
}

type TypeDefKind string

func (k TypeDefKind) String() string {
	return string(k)
}

var TypeDefKinds = dagql.NewEnum[TypeDefKind]()

var (
	TypeDefKindString = TypeDefKinds.Register("STRING_KIND", "A string value.")
	_                 = TypeDefKinds.AliasView("STRING", "STRING_KIND", enumView)

	TypeDefKindInteger = TypeDefKinds.Register("INTEGER_KIND", "An integer value.")
	_                  = TypeDefKinds.AliasView("INTEGER", "INTEGER_KIND", enumView)

	TypeDefKindFloat = TypeDefKinds.Register("FLOAT_KIND", "A float value.")
	_                = TypeDefKinds.AliasView("FLOAT", "FLOAT_KIND", enumView)

	TypeDefKindBoolean = TypeDefKinds.Register("BOOLEAN_KIND", "A boolean value.")
	_                  = TypeDefKinds.AliasView("BOOLEAN", "BOOLEAN_KIND", enumView)

	TypeDefKindScalar = TypeDefKinds.Register("SCALAR_KIND", "A scalar value of any basic kind.")
	_                 = TypeDefKinds.AliasView("SCALAR", "SCALAR_KIND", enumView)

	TypeDefKindList = TypeDefKinds.Register("LIST_KIND",
		"Always paired with a ListTypeDef.",
		"A list of values all having the same type.")
	_ = TypeDefKinds.AliasView("LIST", "LIST_KIND", enumView)

	TypeDefKindObject = TypeDefKinds.Register("OBJECT_KIND",
		"Always paired with an ObjectTypeDef.",
		"A named type defined in the GraphQL schema, with fields and functions.")
	_ = TypeDefKinds.AliasView("OBJECT", "OBJECT_KIND", enumView)

	TypeDefKindInterface = TypeDefKinds.Register("INTERFACE_KIND",
		"Always paired with an InterfaceTypeDef.",
		`A named type of functions that can be matched+implemented by other objects+interfaces.`)
	_ = TypeDefKinds.AliasView("INTERFACE", "INTERFACE_KIND", enumView)

	TypeDefKindInput = TypeDefKinds.Register("INPUT_KIND",
		`A graphql input type, used only when representing the core API via TypeDefs.`)
	_ = TypeDefKinds.AliasView("INPUT", "INPUT_KIND", enumView)

	TypeDefKindVoid = TypeDefKinds.Register("VOID_KIND",
		"A special kind used to signify that no value is returned.",
		`This is used for functions that have no return value. The outer TypeDef
		specifying this Kind is always Optional, as the Void is never actually
		represented.`,
	)
	_ = TypeDefKinds.AliasView("VOID", "VOID_KIND", enumView)

	TypeDefKindEnum = TypeDefKinds.Register("ENUM_KIND",
		"A GraphQL enum type and its values",
		"Always paired with an EnumTypeDef.",
	)
	_ = TypeDefKinds.AliasView("ENUM", "ENUM_KIND", enumView)
)

func (k TypeDefKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "TypeDefKind",
		NonNull:   true,
	}
}

func (k TypeDefKind) TypeDescription() string {
	return `Distinguishes the different kinds of TypeDefs.`
}

func (k TypeDefKind) Decoder() dagql.InputDecoder {
	return TypeDefKinds
}

func (k TypeDefKind) ToLiteral() call.Literal {
	return TypeDefKinds.Literal(k)
}

type FunctionCall struct {
	Name       string                  `field:"true" doc:"The name of the function being called."`
	ParentName string                  `field:"true" doc:"The name of the parent object of the function being called. If the function is top-level to the module, this is the name of the module."`
	Parent     JSON                    `field:"true" doc:"The value of the parent object of the function being called. If the function is top-level to the module, this is always an empty object."`
	InputArgs  []*FunctionCallArgValue `field:"true" doc:"The argument values the function is being invoked with."`

	ParentID *call.ID
	EnvID    *call.ID
}

type persistedFunctionCall FunctionCall

var _ dagql.PersistedObject = (*FunctionCall)(nil)
var _ dagql.PersistedObjectDecoder = (*FunctionCall)(nil)

func (*FunctionCall) Type() *ast.Type {
	return &ast.Type{
		NamedType: "FunctionCall",
		NonNull:   true,
	}
}

func (*FunctionCall) TypeDescription() string {
	return "An active function call."
}

func (fnCall *FunctionCall) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	if fnCall == nil {
		return nil, fmt.Errorf("encode persisted function call: nil function call")
	}
	return json.Marshal(persistedFunctionCall(*fnCall))
}

func (*FunctionCall) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var persisted persistedFunctionCall
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted function call payload: %w", err)
	}
	fnCall := FunctionCall(persisted)
	return &fnCall, nil
}

func (fnCall *FunctionCall) ReturnValue(ctx context.Context, val JSON) error {
	// The return is implemented by exporting the result back to the caller's
	// filesystem. This ensures that the result is cached as part of the module
	// function's Exec while also keeping SDKs as agnostic as possible to the
	// format + location of that result.
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("get engine client: %w", err)
	}
	return bk.IOReaderExport(
		ctx,
		bytes.NewReader(val),
		filepath.Join(modMetaDirPath, modMetaOutputPath),
		0o600,
	)
}

func (fnCall *FunctionCall) ReturnError(ctx context.Context, errID dagql.ID[*Error]) error {
	// The return is implemented by exporting the result back to the caller's
	// filesystem. This ensures that the result is cached as part of the module
	// function's Exec while also keeping SDKs as agnostic as possible to the
	// format + location of that result.
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("get engine client: %w", err)
	}
	enc, err := errID.Encode()
	if err != nil {
		return fmt.Errorf("encode error ID: %w", err)
	}
	return bk.IOReaderExport(
		ctx,
		strings.NewReader(enc),
		filepath.Join(modMetaDirPath, modMetaErrorPath),
		0o600,
	)
}

type FunctionCallArgValue struct {
	Name  string `field:"true" doc:"The name of the argument."`
	Value JSON   `field:"true" doc:"The value of the argument represented as a JSON serialized string."`
}

type persistedFunctionCallArgValue FunctionCallArgValue

var _ dagql.PersistedObject = (*FunctionCallArgValue)(nil)
var _ dagql.PersistedObjectDecoder = (*FunctionCallArgValue)(nil)

func (*FunctionCallArgValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "FunctionCallArgValue",
		NonNull:   true,
	}
}

func (*FunctionCallArgValue) TypeDescription() string {
	return "A value passed as a named argument to a function call."
}

func (arg *FunctionCallArgValue) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	if arg == nil {
		return nil, fmt.Errorf("encode persisted function call arg value: nil function call arg value")
	}
	return json.Marshal(persistedFunctionCallArgValue(*arg))
}

func (*FunctionCallArgValue) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var persisted persistedFunctionCallArgValue
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted function call arg value payload: %w", err)
	}
	arg := FunctionCallArgValue(persisted)
	return &arg, nil
}

type SourceMap struct {
	Module   string `field:"true" doc:"The module dependency this was declared in."`
	Filename string `field:"true" doc:"The filename from the module source."`
	Line     int    `field:"true" doc:"The line number within the filename."`
	Column   int    `field:"true" doc:"The column number within the line."`
	URL      string `field:"true" doc:"The URL to the file, if any. This can be used to link to the source map in the browser."`
}

var _ dagql.PersistedObject = (*SourceMap)(nil)
var _ dagql.PersistedObjectDecoder = (*SourceMap)(nil)

func (*SourceMap) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SourceMap",
		NonNull:   true,
	}
}

func (*SourceMap) TypeDescription() string {
	return "Source location information."
}

func (sourceMap *SourceMap) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	if sourceMap == nil {
		return nil, fmt.Errorf("encode persisted source map: nil source map")
	}
	return json.Marshal(encodePersistedSourceMap(sourceMap))
}

func (*SourceMap) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var persisted persistedSourceMap
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted source map payload: %w", err)
	}
	return decodePersistedSourceMap(&persisted), nil
}

func (sourceMap SourceMap) Clone() *SourceMap {
	cp := sourceMap
	return &cp
}

func (sourceMap *SourceMap) TypeDirective() *ast.Directive {
	if sourceMap == nil {
		return nil
	}

	directive := &ast.Directive{
		Name:      "sourceMap",
		Arguments: ast.ArgumentList{},
	}
	if sourceMap.Module != "" {
		directive.Arguments = append(directive.Arguments, &ast.Argument{
			Name: "module",
			Value: &ast.Value{
				Kind: ast.StringValue,
				Raw:  sourceMap.Module,
			},
		})
	}
	if sourceMap.Filename != "" {
		directive.Arguments = append(directive.Arguments, &ast.Argument{
			Name: "filename",
			Value: &ast.Value{
				Kind: ast.StringValue,
				Raw:  sourceMap.Filename,
			},
		})
	}
	if sourceMap.Line != 0 {
		directive.Arguments = append(directive.Arguments, &ast.Argument{
			Name: "line",
			Value: &ast.Value{
				Kind: ast.IntValue,
				Raw:  fmt.Sprint(sourceMap.Line),
			},
		})
	}
	if sourceMap.Column != 0 {
		directive.Arguments = append(directive.Arguments, &ast.Argument{
			Name: "column",
			Value: &ast.Value{
				Kind: ast.IntValue,
				Raw:  fmt.Sprint(sourceMap.Column),
			},
		})
	}
	if sourceMap.URL != "" {
		directive.Arguments = append(directive.Arguments, &ast.Argument{
			Name: "url",
			Value: &ast.Value{
				Kind: ast.StringValue,
				Raw:  sourceMap.URL,
			},
		})
	}
	return directive
}

type persistedSourceMap struct {
	Module   string `json:"module,omitempty"`
	Filename string `json:"filename,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	URL      string `json:"url,omitempty"`
}

type persistedFunctionArg struct {
	Name              string   `json:"name,omitempty"`
	Description       string   `json:"description,omitempty"`
	SourceMapResultID uint64   `json:"sourceMapResultID,omitempty"`
	TypeDefResultID   uint64   `json:"typeDefResultID,omitempty"`
	DefaultValue      JSON     `json:"defaultValue,omitempty"`
	DefaultPath       string   `json:"defaultPath,omitempty"`
	DefaultAddress    string   `json:"defaultAddress,omitempty"`
	Ignore            []string `json:"ignore,omitempty"`
	Deprecated        *string  `json:"deprecated,omitempty"`
	OriginalName      string   `json:"originalName,omitempty"`
}

type persistedFunction struct {
	Name               string              `json:"name,omitempty"`
	Description        string              `json:"description,omitempty"`
	ArgResultIDs       []uint64            `json:"argResultIDs,omitempty"`
	ReturnTypeResultID uint64              `json:"returnTypeResultID,omitempty"`
	Deprecated         *string             `json:"deprecated,omitempty"`
	SourceMapResultID  uint64              `json:"sourceMapResultID,omitempty"`
	CachePolicy        FunctionCachePolicy `json:"cachePolicy,omitempty"`
	CacheTTLSeconds    *int64              `json:"cacheTTLSeconds,omitempty"`
	IsCheck            bool                `json:"isCheck,omitempty"`
	IsGenerator        bool                `json:"isGenerator,omitempty"`
	IsUp               bool                `json:"isUp,omitempty"`
	ParentOriginalName string              `json:"parentOriginalName,omitempty"`
	OriginalName       string              `json:"originalName,omitempty"`
}

type persistedTypeDef struct {
	Kind                TypeDefKind `json:"kind,omitempty"`
	Optional            bool        `json:"optional,omitempty"`
	AsListResultID      uint64      `json:"asListResultID,omitempty"`
	AsObjectResultID    uint64      `json:"asObjectResultID,omitempty"`
	AsInterfaceResultID uint64      `json:"asInterfaceResultID,omitempty"`
	AsInputResultID     uint64      `json:"asInputResultID,omitempty"`
	AsScalarResultID    uint64      `json:"asScalarResultID,omitempty"`
	AsEnumResultID      uint64      `json:"asEnumResultID,omitempty"`
}

type persistedObjectTypeDef struct {
	Name                string   `json:"name,omitempty"`
	Description         string   `json:"description,omitempty"`
	SourceMapResultID   uint64   `json:"sourceMapResultID,omitempty"`
	FieldResultIDs      []uint64 `json:"fieldResultIDs,omitempty"`
	FunctionResultIDs   []uint64 `json:"functionResultIDs,omitempty"`
	ConstructorResultID uint64   `json:"constructorResultID,omitempty"`
	Deprecated          *string  `json:"deprecated,omitempty"`
	SourceModuleName    string   `json:"sourceModuleName,omitempty"`
	OriginalName        string   `json:"originalName,omitempty"`
}

type persistedFieldTypeDef struct {
	Name              string  `json:"name,omitempty"`
	Description       string  `json:"description,omitempty"`
	TypeDefResultID   uint64  `json:"typeDefResultID,omitempty"`
	SourceMapResultID uint64  `json:"sourceMapResultID,omitempty"`
	Deprecated        *string `json:"deprecated,omitempty"`
	OriginalName      string  `json:"originalName,omitempty"`
}

type persistedInterfaceTypeDef struct {
	Name              string   `json:"name,omitempty"`
	Description       string   `json:"description,omitempty"`
	SourceMapResultID uint64   `json:"sourceMapResultID,omitempty"`
	FunctionResultIDs []uint64 `json:"functionResultIDs,omitempty"`
	SourceModuleName  string   `json:"sourceModuleName,omitempty"`
	OriginalName      string   `json:"originalName,omitempty"`
}

type persistedScalarTypeDef struct {
	Name             string `json:"name,omitempty"`
	Description      string `json:"description,omitempty"`
	OriginalName     string `json:"originalName,omitempty"`
	SourceModuleName string `json:"sourceModuleName,omitempty"`
}

type persistedListTypeDef struct {
	ElementTypeDefResultID uint64 `json:"elementTypeDefResultID,omitempty"`
}

type persistedInputTypeDef struct {
	Name           string   `json:"name,omitempty"`
	FieldResultIDs []uint64 `json:"fieldResultIDs,omitempty"`
}

type persistedEnumTypeDef struct {
	Name              string   `json:"name,omitempty"`
	Description       string   `json:"description,omitempty"`
	MemberResultIDs   []uint64 `json:"memberResultIDs,omitempty"`
	SourceMapResultID uint64   `json:"sourceMapResultID,omitempty"`
	SourceModuleName  string   `json:"sourceModuleName,omitempty"`
	OriginalName      string   `json:"originalName,omitempty"`
}

type persistedEnumMemberTypeDef struct {
	Name              string  `json:"name,omitempty"`
	Value             string  `json:"value,omitempty"`
	Description       string  `json:"description,omitempty"`
	SourceMapResultID uint64  `json:"sourceMapResultID,omitempty"`
	Deprecated        *string `json:"deprecated,omitempty"`
	OriginalName      string  `json:"originalName,omitempty"`
}

func encodePersistedSourceMap(sourceMap *SourceMap) *persistedSourceMap {
	if sourceMap == nil {
		return nil
	}
	return &persistedSourceMap{
		Module:   sourceMap.Module,
		Filename: sourceMap.Filename,
		Line:     sourceMap.Line,
		Column:   sourceMap.Column,
		URL:      sourceMap.URL,
	}
}

func decodePersistedSourceMap(sourceMap *persistedSourceMap) *SourceMap {
	if sourceMap == nil {
		return nil
	}
	return &SourceMap{
		Module:   sourceMap.Module,
		Filename: sourceMap.Filename,
		Line:     sourceMap.Line,
		Column:   sourceMap.Column,
		URL:      sourceMap.URL,
	}
}

func encodePersistedFunctionArg(cache dagql.PersistedObjectCache, arg *FunctionArg) (*persistedFunctionArg, error) {
	if arg == nil {
		return nil, nil
	}
	payload := &persistedFunctionArg{
		Name:           arg.Name,
		Description:    arg.Description,
		DefaultValue:   arg.DefaultValue,
		DefaultPath:    arg.DefaultPath,
		DefaultAddress: arg.DefaultAddress,
		Ignore:         append([]string(nil), arg.Ignore...),
		Deprecated:     arg.Deprecated,
		OriginalName:   arg.OriginalName,
	}
	typeDefID, err := encodePersistedObjectRef(cache, arg.TypeDef, "function arg type def")
	if err != nil {
		return nil, err
	}
	payload.TypeDefResultID = typeDefID
	if arg.SourceMap.Valid && arg.SourceMap.Value.Self() != nil {
		sourceMapID, err := encodePersistedObjectRef(cache, arg.SourceMap.Value, "function arg source map")
		if err != nil {
			return nil, err
		}
		payload.SourceMapResultID = sourceMapID
	}
	return payload, nil
}

func decodePersistedFunctionArg(ctx context.Context, dag *dagql.Server, arg *persistedFunctionArg) (*FunctionArg, error) {
	if arg == nil {
		return nil, nil
	}
	typeDef, err := loadPersistedObjectResultByResultID[*TypeDef](ctx, dag, arg.TypeDefResultID, "function arg type def")
	if err != nil {
		return nil, err
	}
	decoded := &FunctionArg{
		Name:           arg.Name,
		Description:    arg.Description,
		TypeDef:        typeDef,
		DefaultValue:   arg.DefaultValue,
		DefaultPath:    arg.DefaultPath,
		DefaultAddress: arg.DefaultAddress,
		Ignore:         append([]string(nil), arg.Ignore...),
		Deprecated:     arg.Deprecated,
		OriginalName:   arg.OriginalName,
	}
	if arg.SourceMapResultID != 0 {
		sourceMap, err := loadPersistedObjectResultByResultID[*SourceMap](ctx, dag, arg.SourceMapResultID, "function arg source map")
		if err != nil {
			return nil, err
		}
		decoded.SourceMap = dagql.NonNull(sourceMap)
	}
	return decoded, nil
}

func encodePersistedFunction(cache dagql.PersistedObjectCache, fn *Function) (*persistedFunction, error) {
	if fn == nil {
		return nil, nil
	}
	payload := &persistedFunction{
		Name:               fn.Name,
		Description:        fn.Description,
		Deprecated:         fn.Deprecated,
		CachePolicy:        fn.CachePolicy,
		IsCheck:            fn.IsCheck,
		IsGenerator:        fn.IsGenerator,
		IsUp:               fn.IsUp,
		ParentOriginalName: fn.ParentOriginalName,
		OriginalName:       fn.OriginalName,
	}
	returnTypeID, err := encodePersistedObjectRef(cache, fn.ReturnType, "function return type")
	if err != nil {
		return nil, err
	}
	payload.ReturnTypeResultID = returnTypeID
	if fn.SourceMap.Valid && fn.SourceMap.Value.Self() != nil {
		sourceMapID, err := encodePersistedObjectRef(cache, fn.SourceMap.Value, "function source map")
		if err != nil {
			return nil, err
		}
		payload.SourceMapResultID = sourceMapID
	}
	if fn.CacheTTLSeconds.Valid {
		ttl := int64(fn.CacheTTLSeconds.Value)
		payload.CacheTTLSeconds = &ttl
	}
	payload.ArgResultIDs = make([]uint64, 0, len(fn.Args))
	for _, arg := range fn.Args {
		argID, err := encodePersistedObjectRef(cache, arg, "function arg")
		if err != nil {
			return nil, err
		}
		payload.ArgResultIDs = append(payload.ArgResultIDs, argID)
	}
	return payload, nil
}

func decodePersistedFunction(ctx context.Context, dag *dagql.Server, fn *persistedFunction) (*Function, error) {
	if fn == nil {
		return nil, nil
	}
	returnType, err := loadPersistedObjectResultByResultID[*TypeDef](ctx, dag, fn.ReturnTypeResultID, "function return type")
	if err != nil {
		return nil, err
	}
	decoded := &Function{
		Name:               fn.Name,
		Description:        fn.Description,
		ReturnType:         returnType,
		Deprecated:         fn.Deprecated,
		CachePolicy:        fn.CachePolicy,
		IsCheck:            fn.IsCheck,
		IsGenerator:        fn.IsGenerator,
		IsUp:               fn.IsUp,
		ParentOriginalName: fn.ParentOriginalName,
		OriginalName:       fn.OriginalName,
	}
	if fn.SourceMapResultID != 0 {
		sourceMap, err := loadPersistedObjectResultByResultID[*SourceMap](ctx, dag, fn.SourceMapResultID, "function source map")
		if err != nil {
			return nil, err
		}
		decoded.SourceMap = dagql.NonNull(sourceMap)
	}
	if fn.CacheTTLSeconds != nil {
		decoded.CacheTTLSeconds = dagql.NonNull(dagql.Int(*fn.CacheTTLSeconds))
	}
	decoded.Args = make(dagql.ObjectResultArray[*FunctionArg], 0, len(fn.ArgResultIDs))
	for _, argID := range fn.ArgResultIDs {
		arg, err := loadPersistedObjectResultByResultID[*FunctionArg](ctx, dag, argID, "function arg")
		if err != nil {
			return nil, err
		}
		decoded.Args = append(decoded.Args, arg)
	}
	return decoded, nil
}

func encodePersistedTypeDef(cache dagql.PersistedObjectCache, typeDef *TypeDef) (*persistedTypeDef, error) {
	if typeDef == nil {
		return nil, nil
	}
	payload := &persistedTypeDef{
		Kind:     typeDef.Kind,
		Optional: typeDef.Optional,
	}
	if typeDef.AsList.Valid {
		resultID, err := encodePersistedObjectRef(cache, typeDef.AsList.Value, "typedef list")
		if err != nil {
			return nil, err
		}
		payload.AsListResultID = resultID
	}
	if typeDef.AsObject.Valid {
		resultID, err := encodePersistedObjectRef(cache, typeDef.AsObject.Value, "typedef object")
		if err != nil {
			return nil, err
		}
		payload.AsObjectResultID = resultID
	}
	if typeDef.AsInterface.Valid {
		resultID, err := encodePersistedObjectRef(cache, typeDef.AsInterface.Value, "typedef interface")
		if err != nil {
			return nil, err
		}
		payload.AsInterfaceResultID = resultID
	}
	if typeDef.AsInput.Valid {
		resultID, err := encodePersistedObjectRef(cache, typeDef.AsInput.Value, "typedef input")
		if err != nil {
			return nil, err
		}
		payload.AsInputResultID = resultID
	}
	if typeDef.AsScalar.Valid {
		resultID, err := encodePersistedObjectRef(cache, typeDef.AsScalar.Value, "typedef scalar")
		if err != nil {
			return nil, err
		}
		payload.AsScalarResultID = resultID
	}
	if typeDef.AsEnum.Valid {
		resultID, err := encodePersistedObjectRef(cache, typeDef.AsEnum.Value, "typedef enum")
		if err != nil {
			return nil, err
		}
		payload.AsEnumResultID = resultID
	}
	return payload, nil
}

func decodePersistedTypeDef(ctx context.Context, dag *dagql.Server, typeDef *persistedTypeDef) (*TypeDef, error) {
	if typeDef == nil {
		return nil, nil
	}
	decoded := &TypeDef{
		Kind:     typeDef.Kind,
		Optional: typeDef.Optional,
	}
	if typeDef.AsListResultID != 0 {
		list, err := loadPersistedObjectResultByResultID[*ListTypeDef](ctx, dag, typeDef.AsListResultID, "typedef list")
		if err != nil {
			return nil, err
		}
		decoded.AsList = dagql.NonNull(list)
	}
	if typeDef.AsObjectResultID != 0 {
		obj, err := loadPersistedObjectResultByResultID[*ObjectTypeDef](ctx, dag, typeDef.AsObjectResultID, "typedef object")
		if err != nil {
			return nil, err
		}
		decoded.AsObject = dagql.NonNull(obj)
	}
	if typeDef.AsInterfaceResultID != 0 {
		iface, err := loadPersistedObjectResultByResultID[*InterfaceTypeDef](ctx, dag, typeDef.AsInterfaceResultID, "typedef interface")
		if err != nil {
			return nil, err
		}
		decoded.AsInterface = dagql.NonNull(iface)
	}
	if typeDef.AsInputResultID != 0 {
		input, err := loadPersistedObjectResultByResultID[*InputTypeDef](ctx, dag, typeDef.AsInputResultID, "typedef input")
		if err != nil {
			return nil, err
		}
		decoded.AsInput = dagql.NonNull(input)
	}
	if typeDef.AsScalarResultID != 0 {
		scalar, err := loadPersistedObjectResultByResultID[*ScalarTypeDef](ctx, dag, typeDef.AsScalarResultID, "typedef scalar")
		if err != nil {
			return nil, err
		}
		decoded.AsScalar = dagql.NonNull(scalar)
	}
	if typeDef.AsEnumResultID != 0 {
		enum, err := loadPersistedObjectResultByResultID[*EnumTypeDef](ctx, dag, typeDef.AsEnumResultID, "typedef enum")
		if err != nil {
			return nil, err
		}
		decoded.AsEnum = dagql.NonNull(enum)
	}
	return decoded.syncName(), nil
}

func encodePersistedObjectTypeDef(cache dagql.PersistedObjectCache, obj *ObjectTypeDef) (*persistedObjectTypeDef, error) {
	if obj == nil {
		return nil, nil
	}
	payload := &persistedObjectTypeDef{
		Name:              obj.Name,
		Description:       obj.Description,
		Deprecated:        obj.Deprecated,
		SourceModuleName:  obj.SourceModuleName,
		OriginalName:      obj.OriginalName,
		FieldResultIDs:    make([]uint64, 0, len(obj.Fields)),
		FunctionResultIDs: make([]uint64, 0, len(obj.Functions)),
	}
	if obj.SourceMap.Valid && obj.SourceMap.Value.Self() != nil {
		sourceMapID, err := encodePersistedObjectRef(cache, obj.SourceMap.Value, "object typedef source map")
		if err != nil {
			return nil, err
		}
		payload.SourceMapResultID = sourceMapID
	}
	for _, field := range obj.Fields {
		fieldID, err := encodePersistedObjectRef(cache, field, "object typedef field")
		if err != nil {
			return nil, err
		}
		payload.FieldResultIDs = append(payload.FieldResultIDs, fieldID)
	}
	for _, fn := range obj.Functions {
		fnID, err := encodePersistedObjectRef(cache, fn, "object typedef function")
		if err != nil {
			return nil, err
		}
		payload.FunctionResultIDs = append(payload.FunctionResultIDs, fnID)
	}
	if obj.Constructor.Valid {
		constructorID, err := encodePersistedObjectRef(cache, obj.Constructor.Value, "object typedef constructor")
		if err != nil {
			return nil, err
		}
		payload.ConstructorResultID = constructorID
	}
	return payload, nil
}

func decodePersistedObjectTypeDef(ctx context.Context, dag *dagql.Server, obj *persistedObjectTypeDef) (*ObjectTypeDef, error) {
	if obj == nil {
		return nil, nil
	}
	decoded := &ObjectTypeDef{
		Name:             obj.Name,
		Description:      obj.Description,
		Deprecated:       obj.Deprecated,
		SourceModuleName: obj.SourceModuleName,
		OriginalName:     obj.OriginalName,
		Fields:           make(dagql.ObjectResultArray[*FieldTypeDef], 0, len(obj.FieldResultIDs)),
		Functions:        make(dagql.ObjectResultArray[*Function], 0, len(obj.FunctionResultIDs)),
	}
	if obj.SourceMapResultID != 0 {
		sourceMap, err := loadPersistedObjectResultByResultID[*SourceMap](ctx, dag, obj.SourceMapResultID, "object typedef source map")
		if err != nil {
			return nil, err
		}
		decoded.SourceMap = dagql.NonNull(sourceMap)
	}
	for _, fieldID := range obj.FieldResultIDs {
		field, err := loadPersistedObjectResultByResultID[*FieldTypeDef](ctx, dag, fieldID, "object typedef field")
		if err != nil {
			return nil, err
		}
		decoded.Fields = append(decoded.Fields, field)
	}
	for _, fnID := range obj.FunctionResultIDs {
		fn, err := loadPersistedObjectResultByResultID[*Function](ctx, dag, fnID, "object typedef function")
		if err != nil {
			return nil, err
		}
		decoded.Functions = append(decoded.Functions, fn)
	}
	if obj.ConstructorResultID != 0 {
		constructor, err := loadPersistedObjectResultByResultID[*Function](ctx, dag, obj.ConstructorResultID, "object typedef constructor")
		if err != nil {
			return nil, err
		}
		decoded.Constructor = dagql.NonNull(constructor)
	}
	return decoded, nil
}

func encodePersistedFieldTypeDef(cache dagql.PersistedObjectCache, field *FieldTypeDef) (*persistedFieldTypeDef, error) {
	if field == nil {
		return nil, nil
	}
	payload := &persistedFieldTypeDef{
		Name:         field.Name,
		Description:  field.Description,
		Deprecated:   field.Deprecated,
		OriginalName: field.OriginalName,
	}
	typeDefID, err := encodePersistedObjectRef(cache, field.TypeDef, "field typedef type")
	if err != nil {
		return nil, err
	}
	payload.TypeDefResultID = typeDefID
	if field.SourceMap.Valid && field.SourceMap.Value.Self() != nil {
		sourceMapID, err := encodePersistedObjectRef(cache, field.SourceMap.Value, "field typedef source map")
		if err != nil {
			return nil, err
		}
		payload.SourceMapResultID = sourceMapID
	}
	return payload, nil
}

func decodePersistedFieldTypeDef(ctx context.Context, dag *dagql.Server, field *persistedFieldTypeDef) (*FieldTypeDef, error) {
	if field == nil {
		return nil, nil
	}
	typeDef, err := loadPersistedObjectResultByResultID[*TypeDef](ctx, dag, field.TypeDefResultID, "field typedef type")
	if err != nil {
		return nil, err
	}
	decoded := &FieldTypeDef{
		Name:         field.Name,
		Description:  field.Description,
		TypeDef:      typeDef,
		Deprecated:   field.Deprecated,
		OriginalName: field.OriginalName,
	}
	if field.SourceMapResultID != 0 {
		sourceMap, err := loadPersistedObjectResultByResultID[*SourceMap](ctx, dag, field.SourceMapResultID, "field typedef source map")
		if err != nil {
			return nil, err
		}
		decoded.SourceMap = dagql.NonNull(sourceMap)
	}
	return decoded, nil
}

func encodePersistedInterfaceTypeDef(cache dagql.PersistedObjectCache, iface *InterfaceTypeDef) (*persistedInterfaceTypeDef, error) {
	if iface == nil {
		return nil, nil
	}
	payload := &persistedInterfaceTypeDef{
		Name:              iface.Name,
		Description:       iface.Description,
		SourceModuleName:  iface.SourceModuleName,
		OriginalName:      iface.OriginalName,
		FunctionResultIDs: make([]uint64, 0, len(iface.Functions)),
	}
	if iface.SourceMap.Valid && iface.SourceMap.Value.Self() != nil {
		sourceMapID, err := encodePersistedObjectRef(cache, iface.SourceMap.Value, "interface typedef source map")
		if err != nil {
			return nil, err
		}
		payload.SourceMapResultID = sourceMapID
	}
	for _, fn := range iface.Functions {
		fnID, err := encodePersistedObjectRef(cache, fn, "interface typedef function")
		if err != nil {
			return nil, err
		}
		payload.FunctionResultIDs = append(payload.FunctionResultIDs, fnID)
	}
	return payload, nil
}

func decodePersistedInterfaceTypeDef(ctx context.Context, dag *dagql.Server, iface *persistedInterfaceTypeDef) (*InterfaceTypeDef, error) {
	if iface == nil {
		return nil, nil
	}
	decoded := &InterfaceTypeDef{
		Name:             iface.Name,
		Description:      iface.Description,
		SourceModuleName: iface.SourceModuleName,
		OriginalName:     iface.OriginalName,
		Functions:        make(dagql.ObjectResultArray[*Function], 0, len(iface.FunctionResultIDs)),
	}
	if iface.SourceMapResultID != 0 {
		sourceMap, err := loadPersistedObjectResultByResultID[*SourceMap](ctx, dag, iface.SourceMapResultID, "interface typedef source map")
		if err != nil {
			return nil, err
		}
		decoded.SourceMap = dagql.NonNull(sourceMap)
	}
	for _, fnID := range iface.FunctionResultIDs {
		fn, err := loadPersistedObjectResultByResultID[*Function](ctx, dag, fnID, "interface typedef function")
		if err != nil {
			return nil, err
		}
		decoded.Functions = append(decoded.Functions, fn)
	}
	return decoded, nil
}

func encodePersistedScalarTypeDef(typeDef *ScalarTypeDef) *persistedScalarTypeDef {
	if typeDef == nil {
		return nil
	}
	return &persistedScalarTypeDef{
		Name:             typeDef.Name,
		Description:      typeDef.Description,
		OriginalName:     typeDef.OriginalName,
		SourceModuleName: typeDef.SourceModuleName,
	}
}

func decodePersistedScalarTypeDef(typeDef *persistedScalarTypeDef) *ScalarTypeDef {
	if typeDef == nil {
		return nil
	}
	return &ScalarTypeDef{
		Name:             typeDef.Name,
		Description:      typeDef.Description,
		OriginalName:     typeDef.OriginalName,
		SourceModuleName: typeDef.SourceModuleName,
	}
}

func encodePersistedListTypeDef(cache dagql.PersistedObjectCache, typeDef *ListTypeDef) (*persistedListTypeDef, error) {
	if typeDef == nil {
		return nil, nil
	}
	elementTypeDefID, err := encodePersistedObjectRef(cache, typeDef.ElementTypeDef, "list typedef element type")
	if err != nil {
		return nil, err
	}
	return &persistedListTypeDef{
		ElementTypeDefResultID: elementTypeDefID,
	}, nil
}

func decodePersistedListTypeDef(ctx context.Context, dag *dagql.Server, typeDef *persistedListTypeDef) (*ListTypeDef, error) {
	if typeDef == nil {
		return nil, nil
	}
	elementTypeDef, err := loadPersistedObjectResultByResultID[*TypeDef](ctx, dag, typeDef.ElementTypeDefResultID, "list typedef element type")
	if err != nil {
		return nil, err
	}
	return &ListTypeDef{
		ElementTypeDef: elementTypeDef,
	}, nil
}

func encodePersistedInputTypeDef(cache dagql.PersistedObjectCache, typeDef *InputTypeDef) (*persistedInputTypeDef, error) {
	if typeDef == nil {
		return nil, nil
	}
	payload := &persistedInputTypeDef{
		Name:           typeDef.Name,
		FieldResultIDs: make([]uint64, 0, len(typeDef.Fields)),
	}
	for _, field := range typeDef.Fields {
		fieldID, err := encodePersistedObjectRef(cache, field, "input typedef field")
		if err != nil {
			return nil, err
		}
		payload.FieldResultIDs = append(payload.FieldResultIDs, fieldID)
	}
	return payload, nil
}

func decodePersistedInputTypeDef(ctx context.Context, dag *dagql.Server, typeDef *persistedInputTypeDef) (*InputTypeDef, error) {
	if typeDef == nil {
		return nil, nil
	}
	decoded := &InputTypeDef{
		Name:   typeDef.Name,
		Fields: make(dagql.ObjectResultArray[*FieldTypeDef], 0, len(typeDef.FieldResultIDs)),
	}
	for _, fieldID := range typeDef.FieldResultIDs {
		field, err := loadPersistedObjectResultByResultID[*FieldTypeDef](ctx, dag, fieldID, "input typedef field")
		if err != nil {
			return nil, err
		}
		decoded.Fields = append(decoded.Fields, field)
	}
	return decoded, nil
}

func encodePersistedEnumTypeDef(cache dagql.PersistedObjectCache, enum *EnumTypeDef) (*persistedEnumTypeDef, error) {
	if enum == nil {
		return nil, nil
	}
	payload := &persistedEnumTypeDef{
		Name:             enum.Name,
		Description:      enum.Description,
		SourceModuleName: enum.SourceModuleName,
		OriginalName:     enum.OriginalName,
		MemberResultIDs:  make([]uint64, 0, len(enum.Members)),
	}
	if enum.SourceMap.Valid && enum.SourceMap.Value.Self() != nil {
		sourceMapID, err := encodePersistedObjectRef(cache, enum.SourceMap.Value, "enum typedef source map")
		if err != nil {
			return nil, err
		}
		payload.SourceMapResultID = sourceMapID
	}
	for _, member := range enum.Members {
		memberID, err := encodePersistedObjectRef(cache, member, "enum typedef member")
		if err != nil {
			return nil, err
		}
		payload.MemberResultIDs = append(payload.MemberResultIDs, memberID)
	}
	return payload, nil
}

func decodePersistedEnumTypeDef(ctx context.Context, dag *dagql.Server, enum *persistedEnumTypeDef) (*EnumTypeDef, error) {
	if enum == nil {
		return nil, nil
	}
	decoded := &EnumTypeDef{
		Name:             enum.Name,
		Description:      enum.Description,
		SourceModuleName: enum.SourceModuleName,
		OriginalName:     enum.OriginalName,
		Members:          make(dagql.ObjectResultArray[*EnumMemberTypeDef], 0, len(enum.MemberResultIDs)),
	}
	if enum.SourceMapResultID != 0 {
		sourceMap, err := loadPersistedObjectResultByResultID[*SourceMap](ctx, dag, enum.SourceMapResultID, "enum typedef source map")
		if err != nil {
			return nil, err
		}
		decoded.SourceMap = dagql.NonNull(sourceMap)
	}
	for _, memberID := range enum.MemberResultIDs {
		member, err := loadPersistedObjectResultByResultID[*EnumMemberTypeDef](ctx, dag, memberID, "enum typedef member")
		if err != nil {
			return nil, err
		}
		decoded.Members = append(decoded.Members, member)
	}
	return decoded, nil
}

func encodePersistedEnumMemberTypeDef(cache dagql.PersistedObjectCache, member *EnumMemberTypeDef) (*persistedEnumMemberTypeDef, error) {
	if member == nil {
		return nil, nil
	}
	payload := &persistedEnumMemberTypeDef{
		Name:         member.Name,
		Value:        member.Value,
		Description:  member.Description,
		Deprecated:   member.Deprecated,
		OriginalName: member.OriginalName,
	}
	if member.SourceMap.Valid && member.SourceMap.Value.Self() != nil {
		sourceMapID, err := encodePersistedObjectRef(cache, member.SourceMap.Value, "enum member source map")
		if err != nil {
			return nil, err
		}
		payload.SourceMapResultID = sourceMapID
	}
	return payload, nil
}

func decodePersistedEnumMemberTypeDef(ctx context.Context, dag *dagql.Server, member *persistedEnumMemberTypeDef) (*EnumMemberTypeDef, error) {
	if member == nil {
		return nil, nil
	}
	decoded := &EnumMemberTypeDef{
		Name:         member.Name,
		Value:        member.Value,
		Description:  member.Description,
		Deprecated:   member.Deprecated,
		OriginalName: member.OriginalName,
	}
	if member.SourceMapResultID != 0 {
		sourceMap, err := loadPersistedObjectResultByResultID[*SourceMap](ctx, dag, member.SourceMapResultID, "enum member source map")
		if err != nil {
			return nil, err
		}
		decoded.SourceMap = dagql.NonNull(sourceMap)
	}
	return decoded, nil
}
