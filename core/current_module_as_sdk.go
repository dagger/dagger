package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// CurrentModuleAsSDK exposes, to the currently executing module, the SDK-role
// data its workspace install entry carries: the modules and clients this SDK
// authors and manages. It lets SDK generators discover the workspace-managed
// modules/clients they own from the engine's source of truth
// ([[modules.<name>.as-sdk.modules]] / .clients) instead of scanning the
// workspace filesystem for legacy dagger.json files.
type CurrentModuleAsSDK struct {
	// Name is the user-facing SDK name (the as-sdk `name`, falling back to the
	// workspace install entry name).
	Name string `field:"true" doc:"The user-facing name of this SDK in the workspace."`

	// Modules lists the workspace-local modules this SDK authors and manages.
	Modules []*CurrentModuleAsSDKModule

	// Clients lists the generated clients this SDK produces in the workspace.
	Clients []*CurrentModuleAsSDKClient
}

var _ dagql.PersistedObject = (*CurrentModuleAsSDK)(nil)
var _ dagql.PersistedObjectDecoder = (*CurrentModuleAsSDK)(nil)

func (*CurrentModuleAsSDK) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CurrentModuleAsSDK",
		NonNull:   true,
	}
}

func (*CurrentModuleAsSDK) TypeDescription() string {
	return "The SDK-role data for the currently executing module, as installed in the active workspace."
}

func (s *CurrentModuleAsSDK) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if s == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted current module as-sdk: nil value")
	}
	return encodePersistedObjectPayload(s)
}

func (*CurrentModuleAsSDK) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var s CurrentModuleAsSDK
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, fmt.Errorf("decode persisted current module as-sdk payload: %w", err)
	}
	return &s, nil
}

// CurrentModuleAsSDKModule is one workspace-local module managed by the current
// SDK, mirroring a [[modules.<name>.as-sdk.modules]] entry.
type CurrentModuleAsSDKModule struct {
	Path string `field:"true" doc:"Workspace-root-relative path to the managed module."`
}

var _ dagql.PersistedObject = (*CurrentModuleAsSDKModule)(nil)
var _ dagql.PersistedObjectDecoder = (*CurrentModuleAsSDKModule)(nil)

func (*CurrentModuleAsSDKModule) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CurrentModuleAsSDKModule",
		NonNull:   true,
	}
}

func (*CurrentModuleAsSDKModule) TypeDescription() string {
	return "A workspace-local module managed by the current SDK."
}

func (m *CurrentModuleAsSDKModule) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if m == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted current module as-sdk module: nil value")
	}
	return encodePersistedObjectPayload(m)
}

func (*CurrentModuleAsSDKModule) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var m CurrentModuleAsSDKModule
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, fmt.Errorf("decode persisted current module as-sdk module payload: %w", err)
	}
	return &m, nil
}

// CurrentModuleAsSDKClient is one generated client the current SDK produces in
// the workspace, mirroring a [[modules.<name>.as-sdk.clients]] entry.
type CurrentModuleAsSDKClient struct {
	Path   string `field:"true" doc:"Workspace-root-relative path of the generated client."`
	Module string `field:"true" doc:"The module the client is bound to (workspace-relative path or canonical ref)."`
	Pin    string `field:"true" doc:"The pinned version of the bound module, if any."`

	// BoundWorkspace is the Workspace asSDK was called on — the one whose config
	// named this client. Its moduleSource field resolves the bound module against
	// this workspace rather than the session's ambient one, so a client authored
	// by a dependency-driven or overlaid SDK still resolves correctly. Persisted
	// by result ID (persistedCurrentModuleAsSDKClientPayload.BoundWorkspaceResultID),
	// the same way groups persist their bound workspace, so a reloaded client
	// still resolves against the right workspace.
	BoundWorkspace dagql.ObjectResult[*Workspace] `json:"-"`
}

var _ dagql.PersistedObject = (*CurrentModuleAsSDKClient)(nil)
var _ dagql.PersistedObjectDecoder = (*CurrentModuleAsSDKClient)(nil)
var _ dagql.HasDependencyResults = (*CurrentModuleAsSDKClient)(nil)

// persistedCurrentModuleAsSDKClientPayload persists a client's fields plus a
// reference to its bound workspace (by result ID, since the workspace is a full
// object result rather than an inline value).
type persistedCurrentModuleAsSDKClientPayload struct {
	Path                   string `json:"path,omitempty"`
	Module                 string `json:"module,omitempty"`
	Pin                    string `json:"pin,omitempty"`
	BoundWorkspaceResultID uint64 `json:"boundWorkspaceResultID,omitempty"`
}

func (*CurrentModuleAsSDKClient) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CurrentModuleAsSDKClient",
		NonNull:   true,
	}
}

func (*CurrentModuleAsSDKClient) TypeDescription() string {
	return "A generated client the current SDK produces in the workspace."
}

func (c *CurrentModuleAsSDKClient) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if c == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted current module as-sdk client: nil value")
	}
	payload := persistedCurrentModuleAsSDKClientPayload{
		Path:   c.Path,
		Module: c.Module,
		Pin:    c.Pin,
	}
	if c.BoundWorkspace.Self() != nil {
		wsID, err := encodePersistedObjectRef(cache, c.BoundWorkspace, "bound workspace")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.BoundWorkspaceResultID = wsID
	}
	return encodePersistedObjectPayload(payload)
}

func (*CurrentModuleAsSDKClient) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedCurrentModuleAsSDKClientPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted current module as-sdk client payload: %w", err)
	}
	c := &CurrentModuleAsSDKClient{
		Path:   persisted.Path,
		Module: persisted.Module,
		Pin:    persisted.Pin,
	}
	if persisted.BoundWorkspaceResultID != 0 {
		ws, err := loadPersistedObjectResultByResultID[*Workspace](ctx, dag, persisted.BoundWorkspaceResultID, "bound workspace")
		if err != nil {
			return nil, err
		}
		c.BoundWorkspace = ws
	}
	return c, nil
}

// AttachDependencyResults attaches the bound workspace so it becomes cache-backed
// and its result ID resolves when the client is persisted (EncodePersistedObject)
// and reloaded.
func (c *CurrentModuleAsSDKClient) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if c == nil || c.BoundWorkspace.Self() == nil {
		return nil, nil
	}
	attached, err := attach(c.BoundWorkspace)
	if err != nil {
		return nil, fmt.Errorf("attach bound workspace: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Workspace])
	if !ok {
		return nil, fmt.Errorf("attach bound workspace: unexpected result %T", attached)
	}
	c.BoundWorkspace = typed
	return []dagql.AnyResult{typed}, nil
}
