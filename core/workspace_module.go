package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
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
