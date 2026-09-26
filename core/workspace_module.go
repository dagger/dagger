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

func (m *WorkspaceModule) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = enc
	if m == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace module: nil workspace module")
	}
	return encodePersistedObjectPayload(m)
}

func (*WorkspaceModule) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dec
	var m WorkspaceModule
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, fmt.Errorf("decode persisted workspace module payload: %w", err)
	}
	return &m, nil
}

// WorkspaceModuleSetting describes one constructor-backed module setting.
type WorkspaceModuleSetting struct {
	Key          string `field:"true" doc:"The setting key."`
	Value        string `field:"true" doc:"The value stored in workspace config after applying the selected workspace environment, or empty when unset."`
	Description  string `field:"true" doc:"The constructor argument description."`
	DefaultValue string `field:"true" doc:"The constructor argument's declared default, formatted like value, or empty when the argument has no default."`
	IsString     bool   `field:"true" doc:"Whether the setting is a string argument, stored as a TOML string even when the value reads as a number or boolean."`
	IsList       bool   `field:"true" doc:"Whether the setting accepts a list of values."`
	IsObject     bool   `field:"true" doc:"Whether the setting is an object type resolved from an address string (Container, Directory, File, Secret, Service, ...), which may be a module reference."`
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

func (s *WorkspaceModuleSetting) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = enc
	if s == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace module setting: nil workspace module setting")
	}
	return encodePersistedObjectPayload(s)
}

func (*WorkspaceModuleSetting) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dec
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
	Workspace dagql.ObjectResult[*Workspace] `json:"-"`
	Name      string                         `field:"true" doc:"The user-facing SDK name."`
	Ref       string                         `field:"true" doc:"The module reference this SDK was installed from."`
	Modules   []*WorkspaceModule             `field:"true" doc:"Modules authored with this SDK."`
	Clients   []*WorkspaceModule             `field:"true" doc:"Clients generated with this SDK."`
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

type persistedWorkspaceSDK struct {
	*WorkspaceSDK
	WorkspaceResultID uint64
}

func (s *WorkspaceSDK) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	var id uint64
	var err error
	if s.Workspace.Self() != nil {
		id, err = encodePersistedObjectRef(enc, s.Workspace, "SDK workspace")
	}
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return encodePersistedObjectPayload(persistedWorkspaceSDK{WorkspaceSDK: s, WorkspaceResultID: id})
}

func (*WorkspaceSDK) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var saved persistedWorkspaceSDK
	if err := json.Unmarshal(payload, &saved); err != nil {
		return nil, err
	}
	var err error
	if saved.WorkspaceResultID != 0 {
		saved.WorkspaceSDK.Workspace, err = loadPersistedObjectResultByResultID[*Workspace](ctx, dec, saved.WorkspaceResultID, "SDK workspace")
	}
	return saved.WorkspaceSDK, err
}

func (s *WorkspaceSDK) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if s.Workspace.Self() == nil {
		return nil, nil
	}
	result, err := attach(s.Workspace)
	if err != nil {
		return nil, err
	}
	s.Workspace = result.(dagql.ObjectResult[*Workspace])
	return []dagql.AnyResult{result}, nil
}

type WorkspaceSDKs []*WorkspaceSDK

func (s WorkspaceSDKs) Sort() {
	sort.Slice(s, func(i, j int) bool {
		return s[i].Name < s[j].Name
	})
}
