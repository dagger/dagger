package core

import "github.com/vektah/gqlparser/v2/ast"

// DagqlWorkspace represents a detected workspace in the dagql schema.
type DagqlWorkspace struct {
	Root       string `field:"true" doc:"Absolute path to the workspace root directory."`
	ConfigPath string `field:"true" doc:"Path to config.toml (empty string if no config exists)."`
	HasConfig  bool   `field:"true" doc:"Whether a config.toml file exists in the workspace."`
}

func (*DagqlWorkspace) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Workspace",
		NonNull:   true,
	}
}

func (*DagqlWorkspace) TypeDescription() string {
	return "A Dagger workspace detected from the current working directory."
}
