package core

import (
	"context"
	"encoding/json"
	"fmt"

	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// Workspace represents a detected workspace in the dagql schema.
type Workspace struct {
	// rootfs is the pre-fetched root filesystem for remote workspaces.
	// Internal only — not exposed in GraphQL. Local workspaces resolve
	// directories lazily via per-call host.directory() instead.
	rootfs dagql.ObjectResult[*Directory]

	// compatWorkspace stores the originating compat-workspace projection when
	// this workspace was loaded from a legacy dagger.json instead of an explicit
	// .dagger/config.toml. Internal only.
	compatWorkspace *workspacepkg.CompatWorkspace

	// Path is the workspace directory relative to the workspace boundary.
	Path        string `field:"true" doc:"Workspace directory path relative to the workspace boundary."`
	Address     string `field:"true" doc:"Canonical Dagger address of the workspace directory."`
	Initialized bool   `field:"true" doc:"Whether .dagger/config.toml exists."`
	ConfigPath  string `field:"true" doc:"Path to config.toml relative to the workspace boundary (empty if not initialized)."`
	HasConfig   bool   `field:"true" doc:"Whether a config.toml file exists in the workspace."`

	// Cwd is the current working directory within the workspace. Empty means
	// the workspace directory itself.
	Cwd string

	// ClientID is the ID of the client that created this workspace.
	// Used to route host filesystem operations through the correct session
	// when the workspace is passed to a module function.
	ClientID string `field:"true" doc:"The client ID that owns this workspace's host filesystem."`

	// hostPath is the host filesystem path for the workspace boundary.
	// Internal only (not in GraphQL schema). Empty for remote workspaces.
	// Used by workspace filesystem operations that need host access.
	hostPath string
}

// Rootfs returns the pre-fetched root filesystem directory for remote workspaces.
// Returns a zero value for local workspaces (they resolve lazily).
func (ws *Workspace) Rootfs() dagql.ObjectResult[*Directory] {
	return ws.rootfs
}

// SetRootfs sets the pre-fetched root filesystem (used by remote workspace setup).
func (ws *Workspace) SetRootfs(r dagql.ObjectResult[*Directory]) {
	ws.rootfs = r
}

// HostPath returns the internal host filesystem path for the workspace boundary.
// Returns empty string for remote workspaces (read-only).
func (ws *Workspace) HostPath() string {
	return ws.hostPath
}

// SetHostPath sets the internal host filesystem path.
func (ws *Workspace) SetHostPath(p string) {
	ws.hostPath = p
}

// CompatWorkspace returns the internal compat-workspace provenance for this
// workspace. Nil means this workspace was not loaded from legacy compat mode.
func (ws *Workspace) CompatWorkspace() *workspacepkg.CompatWorkspace {
	return ws.compatWorkspace
}

// SetCompatWorkspace sets the internal compat-workspace provenance.
func (ws *Workspace) SetCompatWorkspace(compat *workspacepkg.CompatWorkspace) {
	ws.compatWorkspace = compat
}

func (*Workspace) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Workspace",
		NonNull:   true,
	}
}

func (*Workspace) TypeDescription() string {
	return "A Dagger workspace detected from the current working directory."
}

var _ dagql.PersistedObject = (*Workspace)(nil)
var _ dagql.PersistedObjectDecoder = (*Workspace)(nil)
var _ dagql.HasDependencyResults = (*Workspace)(nil)

type persistedWorkspacePayload struct {
	RootfsResultID uint64 `json:"rootfsResultID,omitempty"`
	Path           string `json:"path,omitempty"`
	Address        string `json:"address,omitempty"`
	Initialized    bool   `json:"initialized,omitempty"`
	ConfigPath     string `json:"configPath,omitempty"`
	HasConfig      bool   `json:"hasConfig,omitempty"`
	ClientID       string `json:"clientID,omitempty"`
	HostPath       string `json:"hostPath,omitempty"`
}

func (ws *Workspace) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if ws == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace: nil workspace")
	}

	payload := persistedWorkspacePayload{
		Path:        ws.Path,
		Address:     ws.Address,
		Initialized: ws.Initialized,
		ConfigPath:  ws.ConfigPath,
		HasConfig:   ws.HasConfig,
		ClientID:    ws.ClientID,
		HostPath:    ws.hostPath,
	}
	if ws.rootfs.Self() != nil {
		rootfsID, err := encodePersistedObjectRef(cache, ws.rootfs, "workspace rootfs")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.RootfsResultID = rootfsID
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted workspace payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payloadBytes), nil
}

func (*Workspace) DecodePersistedObject(
	ctx context.Context,
	dag *dagql.Server,
	_ uint64,
	_ *dagql.ResultCall,
	payload json.RawMessage,
) (dagql.Typed, error) {
	var persisted persistedWorkspacePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted workspace payload: %w", err)
	}

	var rootfs dagql.ObjectResult[*Directory]
	if persisted.RootfsResultID != 0 {
		var err error
		rootfs, err = loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.RootfsResultID, "workspace rootfs")
		if err != nil {
			return nil, err
		}
	}

	return &Workspace{
		rootfs:      rootfs,
		Path:        persisted.Path,
		Address:     persisted.Address,
		Initialized: persisted.Initialized,
		ConfigPath:  persisted.ConfigPath,
		HasConfig:   persisted.HasConfig,
		ClientID:    persisted.ClientID,
		hostPath:    persisted.HostPath,
	}, nil
}

func (ws *Workspace) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if ws == nil || ws.rootfs.Self() == nil {
		return nil, nil
	}

	attached, err := attach(ws.rootfs)
	if err != nil {
		return nil, fmt.Errorf("attach workspace rootfs: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Directory])
	if !ok {
		return nil, fmt.Errorf("attach workspace rootfs: unexpected result %T", attached)
	}
	ws.rootfs = typed
	return []dagql.AnyResult{typed}, nil
}

func (ws *Workspace) Clone() *Workspace {
	cp := *ws
	return &cp
}

// WorkspaceCwd represents the client's current working directory within a
// detected workspace.
type WorkspaceCwd struct {
	// rootfs is the pre-fetched root filesystem for remote workspaces.
	rootfs dagql.ObjectResult[*Directory]

	// Path is relative to the workspace path. Empty means the workspace path.
	Path string `field:"true" doc:"Location of the current working directory within the workspace, relative to the workspace."`

	WorkspacePath string
	ClientID      string

	// hostPath is the host filesystem path for the workspace boundary.
	hostPath string
}

// Rootfs returns the pre-fetched root filesystem directory for remote workspaces.
func (cwd *WorkspaceCwd) Rootfs() dagql.ObjectResult[*Directory] {
	return cwd.rootfs
}

// SetRootfs sets the pre-fetched root filesystem.
func (cwd *WorkspaceCwd) SetRootfs(r dagql.ObjectResult[*Directory]) {
	cwd.rootfs = r
}

// HostPath returns the internal host filesystem path for the workspace boundary.
func (cwd *WorkspaceCwd) HostPath() string {
	return cwd.hostPath
}

// SetHostPath sets the internal host filesystem path.
func (cwd *WorkspaceCwd) SetHostPath(p string) {
	cwd.hostPath = p
}

func (*WorkspaceCwd) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceCwd",
		NonNull:   true,
	}
}

func (*WorkspaceCwd) TypeDescription() string {
	return "The current working directory within the workspace. This is set to the directory where workspace detection started, and cannot be changed."
}

func (cwd *WorkspaceCwd) Clone() *WorkspaceCwd {
	cp := *cwd
	return &cp
}
