package core

import (
	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// Workspace represents a detected workspace in the dagql schema.
type Workspace struct {
	// rootfs is the pre-fetched root filesystem for remote workspaces.
	// Internal only — not exposed in GraphQL. Local workspaces resolve
	// directories lazily via per-call host.directory() instead.
	rootfs dagql.ObjectResult[*Directory]

	// Path is the workspace location within the rootfs/host root.
	Path        string `field:"true" doc:"Workspace path relative to root."`
	Initialized bool   `field:"true" doc:"Whether .dagger/config.toml exists."`
	ConfigPath  string `field:"true" doc:"Path to config.toml relative to root (empty if not initialized)."`
	HasConfig   bool   `field:"true" doc:"Whether a config.toml file exists in the workspace."`

	// ClientID is the ID of the client that created this workspace.
	// Used to route host filesystem operations through the correct session
	// when the workspace is passed to a module function.
	ClientID string `field:"true" doc:"The client ID that owns this workspace's host filesystem."`

	// hostPath is the host filesystem path for the root.
	// Internal only (not in GraphQL schema). Empty for remote workspaces.
	// Used by mutating operations (init, install, configWrite) that need
	// to write to the host via buildkit client.
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

// HostPath returns the internal host filesystem path for the workspace root.
// Returns empty string for remote workspaces (read-only).
func (ws *Workspace) HostPath() string {
	return ws.hostPath
}

// SetHostPath sets the internal host filesystem path.
func (ws *Workspace) SetHostPath(p string) {
	ws.hostPath = p
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

func (ws *Workspace) Clone() *Workspace {
	cp := *ws
	return &cp
}
