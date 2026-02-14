package core

import (
	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// Workspace represents a detected workspace in the dagql schema.
type Workspace struct {
	// Rootfs is the workspace root filesystem — the outermost boundary
	// for all path resolution. All workspace paths resolve within it.
	// Local: host.directory(gitRoot). Remote: cloned git tree.
	Rootfs dagql.ObjectResult[*Directory] `field:"true" doc:"Root filesystem of the workspace. All paths resolve within it."`

	// Path is the workspace location within Rootfs.
	Path        string `field:"true" doc:"Workspace path relative to Rootfs root."`
	Initialized bool   `field:"true" doc:"Whether .dagger/config.toml exists."`
	ConfigPath  string `field:"true" doc:"Path to config.toml relative to Rootfs root (empty if not initialized)."`
	HasConfig   bool   `field:"true" doc:"Whether a config.toml file exists in the workspace."`

	// ClientID is the ID of the client that created this workspace.
	// Used to route host filesystem operations through the correct session
	// when the workspace is passed to a module function.
	ClientID string `field:"true" doc:"The client ID that owns this workspace's host filesystem."`

	// hostPath is the host filesystem path corresponding to Rootfs.
	// Internal only (not in GraphQL schema). Empty for remote workspaces.
	// Used by mutating operations (init, install, configWrite) that need
	// to write to the host via buildkit client.
	hostPath string
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
