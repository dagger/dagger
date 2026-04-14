package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/util/parallel"
	"github.com/vektah/gqlparser/v2/ast"
)

// Generator represents a generator function
type Generator struct {
	Node      *ModTreeNode `json:"node"`
	Completed bool         `field:"true" doc:"Whether the generator complete"`
	Changes   *Changeset   `json:"changes"`
}

func (*Generator) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Generator",
		NonNull:   true,
	}
}

func (g *Generator) Path() []string {
	return g.Node.Path()
}

func (g *Generator) Description() string {
	return g.Node.Description
}

func (g *Generator) Name() string {
	return g.Node.PathString()
}

func (g *Generator) OriginalModule() *Module {
	return g.Node.OriginalModule
}

func (g *Generator) Clone() *Generator {
	c := *g
	c.Node = g.Node.Clone()
	return &c
}

func (g *Generator) Run(ctx context.Context) (*Generator, error) {
	g = g.Clone()

	cs, _ := g.Node.RunGenerator(ctx, nil, nil) // ignore error as already sent to the trace if needed
	g.Completed = true
	g.Changes = cs
	return g, nil
}

func (g *Generator) RequireChanges(field string) (*Changeset, error) {
	if !g.Completed {
		return nil, fmt.Errorf("generator %q must be run before querying %s", g.Name(), field)
	}
	if g.Changes == nil {
		return nil, fmt.Errorf("generator %q did not produce a changeset result", g.Name())
	}
	return g.Changes, nil
}

type GeneratorGroup struct {
	Node       *ModTreeNode `json:"node"`
	Generators []*Generator `json:"generators"`
}

func NewGeneratorGroup(ctx context.Context, mod *Module, include []string) (*GeneratorGroup, error) {
	rootNode, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	generatorNodes, err := rootNode.RollupGenerator(ctx, include, nil)
	if err != nil {
		return nil, err
	}
	generators := make([]*Generator, 0, len(generatorNodes))

	for _, generatorNode := range generatorNodes {
		generators = append(generators, &Generator{Node: generatorNode})
	}

	return &GeneratorGroup{
		Node:       rootNode,
		Generators: generators,
	}, nil
}

func (*GeneratorGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GeneratorGroup",
		NonNull:   true,
	}
}

func (gg *GeneratorGroup) List(ctx context.Context) []*Generator {
	return gg.Generators
}

// Run all the generators in the group
func (gg *GeneratorGroup) Run(ctx context.Context) (*GeneratorGroup, error) {
	gg = gg.Clone()

	jobs := parallel.New().WithContextualTracer(true)
	for _, generator := range gg.Generators {
		// Reset output fields, in case we're re-running
		generator.Completed = false
		generator.Changes = nil
		jobs = jobs.WithJob(generator.Name(), func(ctx context.Context) error {
			cs, err := generator.Node.RunGenerator(ctx, nil, nil)
			generator.Completed = true
			generator.Changes = cs
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return gg, nil
}

func (gg *GeneratorGroup) IsEmpty(ctx context.Context) (bool, error) {
	for _, g := range gg.Generators {
		changes, err := g.RequireChanges("isEmpty")
		if err != nil {
			return false, err
		}
		if empty, err := changes.IsEmpty(ctx); err != nil {
			return false, err
		} else if !empty {
			return false, nil
		}
	}
	return true, nil
}

func (gg *GeneratorGroup) Changes(ctx context.Context, conflictStrategy WithChangesetsMergeConflict) (*Changeset, error) {
	res, err := NewEmptyChangeset(ctx)
	if err != nil {
		return nil, err
	}
	cs := make([]*Changeset, 0, len(gg.Generators))
	for _, g := range gg.Generators {
		changes, err := g.RequireChanges("changes")
		if err != nil {
			return nil, err
		}
		cs = append(cs, changes)
	}
	return res.WithChangesets(ctx, cs, conflictStrategy)
}

func (gg *GeneratorGroup) Clone() *GeneratorGroup {
	c := *gg
	if gg.Node != nil {
		c.Node = gg.Node.Clone()
	}
	c.Generators = make([]*Generator, len(gg.Generators))
	for i := range c.Generators {
		c.Generators[i] = gg.Generators[i].Clone()
	}
	return &c
}
