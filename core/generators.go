package core

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
	"github.com/vektah/gqlparser/v2/ast"
)

// Generator represents a generator function
type Generator struct {
	Node      *ModTreeNode `json:"node"`
	Completed bool         `field:"true" doc:"Whether the generator complete"`
	Changes   *Changeset   `field:"true" doc:"The generated changeset"`
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

type GeneratorGroup struct {
	Node       *ModTreeNode `json:"node"`
	Generators []*Generator `json:"generators"`
	include    []string
	exclude    []string
}

func NewGeneratorGroup(ctx context.Context, mod *Module, include []string, filters []CollectionFilterInput) (*GeneratorGroup, error) {
	rootNode, err := NewModTree(ctx, mod, NewCollectionFilterSet(filters))
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
		include:    append([]string(nil), include...),
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
		if g.Changes != nil {
			if empty, err := g.Changes.IsEmpty(ctx); err != nil {
				return false, err
			} else if !empty {
				return false, nil
			}
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
		cs = append(cs, g.Changes)
	}
	return res.WithChangesets(ctx, cs, conflictStrategy)
}

func (gg *GeneratorGroup) Clone() *GeneratorGroup {
	c := *gg
	if gg.Node != nil {
		c.Node = gg.Node.Clone()
	}
	c.Generators = make([]*Generator, len(gg.Generators))
	c.include = append([]string(nil), gg.include...)
	c.exclude = append([]string(nil), gg.exclude...)
	for i := range c.Generators {
		c.Generators[i] = gg.Generators[i].Clone()
	}
	return &c
}

func (gg *GeneratorGroup) CollectionFilterValues(ctx context.Context, typeNames []string) ([]*CollectionFilterValues, error) {
	if gg.Node == nil {
		return collectionFilterValuesFromWorkspaceRoots(ctx, typeNames, gg.include, gg.exclude, workspaceGeneratorRoots(gg.Generators))
	}
	return gg.Node.CollectionFilterValues(ctx, typeNames, gg.include, gg.exclude)
}
