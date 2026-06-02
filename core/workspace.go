package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

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

	Address    string `field:"true" doc:"Canonical Dagger address of the workspace location."`
	Cwd        string `field:"true" doc:"Current location within the workspace root. Relative paths in workspace APIs resolve from here."`
	ConfigFile string `field:"true" doc:"Selected native workspace config file relative to the workspace root, if any."`

	// EnvConfigKey is the stable key used to match workspace-scoped user envs.
	EnvConfigKey string `field:"true" doc:"Key used to match workspace-scoped user environment config."`

	// LockFile is the selected lockfile path relative to the workspace root.
	// It is independent from ConfigFile: compat config and missing native config
	// can still have a writable local lockfile.
	LockFile string

	// ClientID is the ID of the client that created this workspace.
	// Used to route host filesystem operations through the correct session
	// when the workspace is passed to a module function.
	ClientID string `field:"true" doc:"The client ID that owns this workspace's host filesystem."`

	// hostPath is the host filesystem path for the workspace boundary.
	// Internal only (not in GraphQL schema). Empty for remote workspaces.
	// Used by workspace filesystem operations that need host access.
	hostPath string

	// localConfigReadOnly marks generated/synthetic local workspaces whose
	// root exists only to keep Workspace APIs total. Workspace-local config
	// mutations must fail; global/user config mutations may still work.
	localConfigReadOnly bool
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
	RootfsResultID  uint64                        `json:"rootfsResultID,omitempty"`
	CompatWorkspace *workspacepkg.CompatWorkspace `json:"compatWorkspace,omitempty"`
	Address         string                        `json:"address,omitempty"`
	Cwd             string                        `json:"cwd,omitempty"`
	ConfigFile      string                        `json:"configFile,omitempty"`
	LockFile        string                        `json:"lockFile,omitempty"`
	ClientID        string                        `json:"clientID,omitempty"`
	HostPath        string                        `json:"hostPath,omitempty"`
	EnvConfigKey    string                        `json:"envConfigKey,omitempty"`
	LocalConfigRO   bool                          `json:"localConfigReadOnly,omitempty"`

	// Decode-only names from main's pre-workspace-selection payload.
	LegacyPath       string `json:"path,omitempty"`
	LegacyConfigPath string `json:"configPath,omitempty"`
}

func (ws *Workspace) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if ws == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace: nil workspace")
	}

	payload := persistedWorkspacePayload{
		CompatWorkspace: ws.compatWorkspace,
		Address:         ws.Address,
		Cwd:             ws.Cwd,
		ConfigFile:      ws.ConfigFile,
		LockFile:        ws.LockFile,
		ClientID:        ws.ClientID,
		HostPath:        ws.hostPath,
		EnvConfigKey:    ws.EnvConfigKey,
		LocalConfigRO:   ws.localConfigReadOnly,
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

	cwd := persisted.Cwd
	if cwd == "" {
		cwd = persisted.LegacyPath
	}
	configFile := persisted.ConfigFile
	if configFile == "" {
		configFile = persisted.LegacyConfigPath
	}
	lockFile := persisted.LockFile
	if lockFile == "" && configFile != "" {
		lockFile = filepath.Join(filepath.Dir(configFile), workspacepkg.LockFileName)
	}

	return &Workspace{
		rootfs:              rootfs,
		compatWorkspace:     persisted.CompatWorkspace,
		Address:             persisted.Address,
		Cwd:                 cwd,
		ConfigFile:          configFile,
		LockFile:            lockFile,
		ClientID:            persisted.ClientID,
		hostPath:            persisted.HostPath,
		EnvConfigKey:        persisted.EnvConfigKey,
		localConfigReadOnly: persisted.LocalConfigRO,
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

func (ws *Workspace) LocalConfigReadOnly() bool {
	return ws.localConfigReadOnly
}

func (ws *Workspace) SetLocalConfigReadOnly(readOnly bool) {
	ws.localConfigReadOnly = readOnly
}
