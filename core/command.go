package core

import (
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// Command describes a configured command. It does not execute it.
type Command struct {
	Args                     []string                     `field:"true" doc:"The command arguments."`
	Env                      []EnvVariable                `field:"true" doc:"Environment variable overrides. Other variables come from the container."`
	Workdir                  dagql.Nullable[dagql.String] `field:"true" doc:"Working directory override. If unset, use the container's working directory."`
	PrivilegedNesting        bool                         `field:"true" doc:"Whether the command has access to Dagger."`
	InsecureRootCapabilities bool                         `field:"true" doc:"Whether the command has all root capabilities."`
}

func (Command) Type() *ast.Type {
	return &ast.Type{NamedType: "Command", NonNull: true}
}

func (Command) TypeDescription() string {
	return "A command's arguments and execution settings."
}

// Shell resolves the shell configuration without evaluating the container.
// Callers must first load the container metadata.
func (container *Container) Shell(batch, defaultNesting bool) Command {
	defaults := container.DefaultTerminalCmd
	args := slices.Clone(defaults.Args)
	if len(args) == 0 {
		args = []string{"sh"}
	}
	if batch {
		if defaults.Batch != nil {
			args = slices.Clone(defaults.Batch)
		} else {
			args = append(args, "-c")
		}
	}
	return Command{
		Args:                     args,
		Env:                      []EnvVariable{},
		PrivilegedNesting:        defaults.ExperimentalPrivilegedNesting.GetOr(dagql.Boolean(defaultNesting)).Bool(),
		InsecureRootCapabilities: bool(defaults.InsecureRootCapabilities.Value),
	}
}
