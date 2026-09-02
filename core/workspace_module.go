package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
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
	Value       string `field:"true" doc:"The address value, e.g. \"sandboxes:go\"."`
	Description string `field:"true" doc:"The function's doc string."`
	TypeName    string `field:"true" name:"type" doc:"Name of the type the address resolves to, e.g. \"Container\"."`
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

// Addresses lists the module's functions loadable as bare "module:function"
// address references (hack/designs/sandboxes.md §5): top-level functions on
// the main object — the only shape resolveModuleRef can load — returning
// typeName and taking no caller-supplied arguments. Engine-supplied ones (an
// auto-injected Workspace, an @agent's base LLM) don't count:
// functionRequiresCallerArgs is the rule, and resolveModuleRef fills both.
// Whether the module can be referenced at all (an entrypoint module's
// functions are hoisted onto the Query root) is the workspace's call, not the
// module's.
//
// typeName may name an interface: objects and interfaces share GraphQL's type
// namespace, so a function returning any implementor is listed too. Which
// types implement which interfaces is a relation of the served schema, not of
// the typedef, so the caller supplies it as implements; a nil checker matches
// by name only. Each address carries the concrete type it resolves to, so a
// caller that asked by interface can pick the right Address loader.
//
// Values are kebab-cased on both segments, matching CLI-facing names;
// resolveModuleRef normalizes with ToLowerCamel, so they round-trip.
func (mod *Module) Addresses(typeName string, implements dagql.ImplementsChecker) WorkspaceAddresses {
	mainObj, ok := mod.MainObject()
	if !ok {
		return nil
	}
	modName := strcase.ToKebab(mod.Name())
	var addresses WorkspaceAddresses
	for _, fnRes := range mainObj.Functions {
		fn := fnRes.Self()
		retType := fn.ReturnType.Self()
		// A list return can't be lifted into a single object; ast.Type.Name
		// would still report the element type's name, so rule lists out first.
		if retType.Kind == TypeDefKindList {
			continue
		}
		retName := retType.ToType().Name()
		if retName != typeName && (implements == nil || !implements(retName, typeName)) {
			continue
		}
		if functionRequiresCallerArgs(fn) {
			continue
		}
		addresses = append(addresses, &WorkspaceAddress{
			Value:       modName + ":" + strcase.ToKebab(fn.Name),
			Description: fn.Description,
			TypeName:    retName,
		})
	}
	return addresses
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
