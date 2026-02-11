package core

import "github.com/vektah/gqlparser/v2/ast"

// Workspace represents a detected workspace in the dagql schema.
type Workspace struct {
	Root       string `field:"true" doc:"Absolute path to the workspace root directory."`
	ConfigPath string `field:"true" doc:"Path to config.toml (empty string if no config exists)."`
	HasConfig  bool   `field:"true" doc:"Whether a config.toml file exists in the workspace."`
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
