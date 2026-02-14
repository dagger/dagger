package core

import "github.com/vektah/gqlparser/v2/ast"

// Workspace represents a detected workspace in the dagql schema.
type Workspace struct {
	SandboxRoot string `field:"true" doc:"Root of the sandbox filesystem (git root or workspace dir)."`
	Path        string `field:"true" doc:"Workspace path relative to sandbox root."`
	Initialized bool   `field:"true" doc:"Whether .dagger/config.toml exists."`
	ConfigPath  string `field:"true" doc:"Path to config.toml relative to sandbox root (empty if not initialized)."`
	HasConfig   bool   `field:"true" doc:"Whether a config.toml file exists in the workspace."`

	// ClientID is the ID of the client that created this workspace.
	// Used to route host filesystem operations through the correct session
	// when the workspace is passed to a module function.
	ClientID string `field:"true" doc:"The client ID that owns this workspace's host filesystem."`
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
