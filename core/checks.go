package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// Check represents a validation check with its result
type Check struct {
	Name         string `field:"true" doc:"The name of the check"`
	Context      string `field:"true" doc:"The context of the check. Can be a remote git address, or a local path`
	Description  string `field:"true" doc:"The description of the check"`
	Success      bool   `json:"success" doc:"Whether the check succeeded`
	Message      string `json:"message" doc:"A message emitted when running the check"`
	ModuleName   string `json:"moduleName"`
	FunctionName string `json:"functionName"`
}

func (*Check) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Check",
		NonNull:   true,
	}
}

// Run executes the check and returns the result
func (c *Check) Run(ctx context.Context) (bool, string, error) {
	//srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return false, err.Error(), nil
	}
	var result any
	err = srv.Select(ctx, srv.Root(), &result,
		dagql.Selector{Field: c.ModuleName},
		dagql.Selector{Field: c.FunctionName},
	)
	if err != nil {
		return false, "", err
	}
	return true, "", nil
}
