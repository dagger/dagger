package core

import (
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

func (ws *Workspace) Clone() *Workspace {
	cp := *ws
	return &cp
}
