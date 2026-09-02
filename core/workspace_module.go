package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// WorkspaceModule describes a module entry in the workspace config.
type WorkspaceModule struct {
	Name       string `field:"true" doc:"The module name."`
	Entrypoint bool   `field:"true" doc:"Whether the module is the workspace entrypoint (functions aliased to Query root)."`
	Source     string `field:"true" doc:"The module source path."`
}

var _ dagql.PersistedObject = (*WorkspaceModule)(nil)
var _ dagql.PersistedObjectDecoder = (*WorkspaceModule)(nil)

func (*WorkspaceModule) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceModule",
		NonNull:   true,
	}
}

func (*WorkspaceModule) TypeDescription() string {
	return "A module entry in the workspace configuration."
}

func (m *WorkspaceModule) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if m == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace module: nil workspace module")
	}
	return encodePersistedObjectPayload(m)
}

func (*WorkspaceModule) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var m WorkspaceModule
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, fmt.Errorf("decode persisted workspace module payload: %w", err)
	}
	return &m, nil
}

// WorkspaceModuleSetting describes one constructor-backed module setting.
type WorkspaceModuleSetting struct {
	Key         string `field:"true" doc:"The setting key."`
	Value       string `field:"true" doc:"The configured value after applying the selected workspace environment, or empty when unset."`
	Description string `field:"true" doc:"The constructor argument description."`
	IsList      bool   `field:"true" doc:"Whether the setting accepts a list of values."`
}

var _ dagql.PersistedObject = (*WorkspaceModuleSetting)(nil)
var _ dagql.PersistedObjectDecoder = (*WorkspaceModuleSetting)(nil)

func (*WorkspaceModuleSetting) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceModuleSetting",
		NonNull:   true,
	}
}

func (*WorkspaceModuleSetting) TypeDescription() string {
	return "A constructor-backed module setting."
}

func (s *WorkspaceModuleSetting) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if s == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace module setting: nil workspace module setting")
	}
	return encodePersistedObjectPayload(s)
}

func (*WorkspaceModuleSetting) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var s WorkspaceModuleSetting
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, fmt.Errorf("decode persisted workspace module setting payload: %w", err)
	}
	return &s, nil
}

type WorkspaceModules []*WorkspaceModule

func (m WorkspaceModules) Sort() {
	sort.Slice(m, func(i, j int) bool {
		return m[i].Name < m[j].Name
	})
}

// WorkspaceAddress describes a module function loadable as an address: a bare
// "module:function" reference that Query.address resolves (see
// hack/designs/sandboxes.md §5).
type WorkspaceAddress struct {
	Value       string   `field:"true" doc:"The address value, e.g. \"sandboxes:go\"."`
	Description string   `field:"true" doc:"The function's doc string."`
	TypeName    string   `field:"true" name:"type" doc:"Name of the type the address resolves to, e.g. \"Container\"."`
	Directives  []string `field:"true" doc:"Names of the directives on the function, e.g. [\"check\"]."`
}

var _ dagql.PersistedObject = (*WorkspaceAddress)(nil)
var _ dagql.PersistedObjectDecoder = (*WorkspaceAddress)(nil)

func (*WorkspaceAddress) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceAddress",
		NonNull:   true,
	}
}

func (*WorkspaceAddress) TypeDescription() string {
	return "A module function loadable as an address."
}

func (a *WorkspaceAddress) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if a == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace address: nil workspace address")
	}
	return encodePersistedObjectPayload(a)
}

func (*WorkspaceAddress) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var a WorkspaceAddress
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, fmt.Errorf("decode persisted workspace address payload: %w", err)
	}
	return &a, nil
}

type WorkspaceAddresses []*WorkspaceAddress

func (a WorkspaceAddresses) Sort() {
	sort.Slice(a, func(i, j int) bool {
		return a[i].Value < a[j].Value
	})
}

// AddressFilter narrows Module.Addresses. Each list is a disjunction over one
// dimension and the lists conjoin: a function is listed when it matches any
// entry of every non-nil list. A nil list does not filter; an empty one is an
// empty disjunction and matches nothing.
type AddressFilter struct {
	// Types the function's return type must be, or implement. Objects and
	// interfaces share GraphQL's type namespace, so an interface name lists
	// functions returning any of its implementors.
	Types []string
	// Directives the function's field definition must carry, by name.
	Directives []string
}

// Validate rejects names the served schema does not declare, so a typo errors
// instead of matching nothing. Types must be objects or interfaces, since
// nothing else can be loaded as an address.
func (f AddressFilter) Validate(srv *dagql.Server) error {
	for _, name := range f.Types {
		if _, ok := srv.ObjectType(name); ok {
			continue
		}
		if _, ok := srv.InterfaceType(name); ok {
			continue
		}
		return fmt.Errorf("unknown object or interface type %q", name)
	}
	if len(f.Directives) > 0 {
		declared := srv.Schema().Directives
		for _, name := range f.Directives {
			if _, ok := declared[name]; !ok {
				return fmt.Errorf("unknown directive %q", name)
			}
		}
	}
	return nil
}

// Addresses lists the module's functions loadable as bare "module:function"
// address references (hack/designs/sandboxes.md §5): top-level functions on
// the main object — the only shape resolveModuleRef can load — taking no
// caller-supplied arguments and passing filter. Engine-supplied arguments (an
// auto-injected Workspace, an @agent's base LLM) don't count:
// functionRequiresCallerArgs is the rule, and resolveModuleRef fills both.
// Whether the module can be referenced at all (an entrypoint module's
// functions are hoisted onto the Query root) is the workspace's call, not the
// module's.
//
// srv is the served schema the addresses resolve against. Which types
// implement which interfaces, and which directives a function's field carries,
// are relations of that schema rather than of the typedef, so both filters and
// both descriptive fields read from it. Each address carries the concrete type
// it resolves to, so a caller that asked by interface can pick the right
// Address loader.
//
// Values are kebab-cased on both segments, matching CLI-facing names;
// resolveModuleRef normalizes with ToLowerCamel, so they round-trip.
func (mod *Module) Addresses(srv *dagql.Server, filter AddressFilter) WorkspaceAddresses {
	mainObj, ok := mod.MainObject()
	if !ok {
		return nil
	}
	objType, _ := srv.ObjectType(mainObj.Name)
	modName := strcase.ToKebab(mod.Name())
	var addresses WorkspaceAddresses
	for _, fnRes := range mainObj.Functions {
		fn := fnRes.Self()
		retType := fn.ReturnType.Self()
		// Only an object can be loaded as an address: every Address loader
		// yields one, so scalars and lists are never listed, whatever the
		// filter says. (A list's ast.Type.Name would still report the element
		// type, so the kind check has to come before any name comparison.)
		if retType.Kind != TypeDefKindObject && retType.Kind != TypeDefKindInterface {
			continue
		}
		retName := retType.ToType().Name()
		if filter.Types != nil && !slices.ContainsFunc(filter.Types, func(name string) bool {
			return name == retName || implementsInterface(srv, retName, name)
		}) {
			continue
		}
		if functionRequiresCallerArgs(fn) {
			continue
		}
		directives := fieldDirectiveNames(objType, fn.Name, srv.View)
		if filter.Directives != nil && !slices.ContainsFunc(filter.Directives, func(name string) bool {
			return slices.Contains(directives, name)
		}) {
			continue
		}
		addresses = append(addresses, &WorkspaceAddress{
			Value:       modName + ":" + strcase.ToKebab(fn.Name),
			Description: fn.Description,
			TypeName:    retName,
			Directives:  directives,
		})
	}
	return addresses
}

// implementsInterface reports whether the served schema records typeName as
// an implementor of ifaceName. dagql matches interfaces structurally when a
// schema is built and notes the implementors on each interface, for core
// interfaces (Syncer, Exportable, Node) and module-defined ones alike.
func implementsInterface(srv *dagql.Server, typeName, ifaceName string) bool {
	iface, ok := srv.InterfaceType(ifaceName)
	return ok && iface.HasImplementor(typeName)
}

// fieldDirectiveNames lists the directives on a field as the schema renders
// them, so a lookup by directive is true to its name: whatever the SDL shows
// on the field, @deprecated and @sourceMap included, is what matches. Always
// non-nil, since the field is served as a non-null list.
func fieldDirectiveNames(objType dagql.ObjectType, field string, view call.View) []string {
	names := []string{}
	if objType == nil {
		return names
	}
	spec, ok := objType.FieldSpec(field, view)
	if !ok {
		return names
	}
	for _, d := range spec.FieldDefinition(view).Directives {
		names = append(names, d.Name)
	}
	return names
}

// WorkspaceSDK describes a module entry installed as an SDK in the workspace
// config.
type WorkspaceSDK struct {
	Name    string             `field:"true" doc:"The user-facing SDK name."`
	Ref     string             `field:"true" doc:"The module reference this SDK was installed from."`
	Modules []*WorkspaceModule `field:"true" doc:"Modules authored with this SDK."`
	Clients []*WorkspaceModule `field:"true" doc:"Clients generated with this SDK."`
}

var _ dagql.PersistedObject = (*WorkspaceSDK)(nil)
var _ dagql.PersistedObjectDecoder = (*WorkspaceSDK)(nil)

func (*WorkspaceSDK) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceSDK",
		NonNull:   true,
	}
}

func (*WorkspaceSDK) TypeDescription() string {
	return "An installed SDK: a module marked for scaffolding other modules and clients."
}

func (s *WorkspaceSDK) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if s == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace SDK: nil workspace SDK")
	}
	return encodePersistedObjectPayload(s)
}

func (*WorkspaceSDK) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var s WorkspaceSDK
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, fmt.Errorf("decode persisted workspace SDK payload: %w", err)
	}
	return &s, nil
}

type WorkspaceSDKs []*WorkspaceSDK

func (s WorkspaceSDKs) Sort() {
	sort.Slice(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})
}
