package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// Generator retains a generation function without running it.
type Generator struct {
	Artifact  *Artifact
	Arguments JSON
	Inputs    []dagql.AnyObjectResult
	Result    dagql.ObjectResult[*Changeset]
}

func NewGenerator(a *Artifact, arguments JSON) (*Generator, error) {
	a = a.Clone()
	if a.TypeName == "Check" && a.Node != nil && a.Node.Name == "stale" && a.Node.Parent != nil && a.Node.Parent.ObjectType() != nil && a.Node.Parent.ObjectType().Name == "Generator" {
		a.Node = a.Node.Parent
		a.Path = a.Path[:len(a.Path)-1]
		a.TypeName, a.Directives = "Generator", a.Node.Directives
		a.setTypeDimension()
	}
	if a.TypeName != "Generator" {
		return nil, fmt.Errorf("expected Generator, got %s", a.TypeName)
	}
	return &Generator{Artifact: a, Arguments: arguments}, nil
}

func (*Generator) Type() *ast.Type { return &ast.Type{NamedType: "Generator", NonNull: true} }
func (*Generator) TypeDescription() string {
	return "A generation function and its staleness check. Reading changeset runs the function."
}
func (g *Generator) Clone() *Generator {
	return &Generator{Artifact: g.Artifact.Clone(), Arguments: g.Arguments, Inputs: slices.Clone(g.Inputs), Result: g.Result}
}
func (g *Generator) AttachDependencyResults(ctx context.Context, owner dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	owned, err := g.Artifact.AttachDependencyResults(ctx, owner, attach)
	if err != nil {
		return nil, err
	}
	for i, input := range g.Inputs {
		result, err := attach(input)
		if err != nil {
			return nil, err
		}
		g.Inputs[i] = result.(dagql.AnyObjectResult)
		owned = append(owned, result)
	}
	if g.Result.Self() != nil {
		result, err := attach(g.Result)
		if err != nil {
			return nil, err
		}
		g.Result = result.(dagql.ObjectResult[*Changeset])
		owned = append(owned, result)
	}
	return owned, nil
}
