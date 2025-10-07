package core

import (
	"github.com/vektah/gqlparser/v2/ast"
)

type EnvVariable struct {
	Name  string `field:"true" doc:"The environment variable name."`
	Value string `field:"true" doc:"The environment variable value."`
}

func (EnvVariable) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EnvVariable",
		NonNull:   true,
	}
}

func (EnvVariable) TypeDescription() string {
	return "An environment variable name and value."
}

func (EnvVariable) Description() string {
	return "A simple key value object that represents an environment variable."
}
