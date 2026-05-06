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

	Address         string  `field:"true" doc:"Canonical Dagger address of the workspace location."`
	Cwd             string  `field:"true" doc:"Current location within the workspace root. Relative paths in workspace APIs resolve from here."`
	ConfigDirectory *string `field:"true" doc:"Selected workspace config directory relative to the workspace root, if any."`
	ConfigFile      *string `field:"true" doc:"Selected workspace config file relative to the workspace root, if any."`
	HasConfig       bool    `field:"true" doc:"Whether a workspace config file exists."`

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
