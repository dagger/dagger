package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// Generator represents a generator function
type Generator struct {
	Path        []string   `field:"true" doc:"The path of the generator within its module"`
	Description string     `field:"true" doc:"The description of the generator"`
	Completed   bool       `field:"true" doc:"Whether the generator complete"`
	Changes     *Changeset `field:"true" doc:"The generated changeset"`
	Module      *Module
}

func (*Generator) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Generator",
		NonNull:   true,
	}
}

type GeneratorGroup struct {
	Module     *Module      `json:"modules"`
	Generators []*Generator `json:"generators"`
}

func (*GeneratorGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GeneratorGroup",
		NonNull:   true,
	}
}

// Return the generator name matching CLI case
func (g *Generator) CliName() string {
	path := g.Path
	for i := range path {
		path[i] = strcase.ToKebab(path[i])
	}
	return strings.Join(path, "/")
}

func (g *Generator) GqlName() string {
	path := g.Path
	for i := range path {
		path[i] = gqlFieldName(path[i])
	}
	return strings.Join(path, "/")
}

func (g *Generator) Match(include []string) (bool, error) {
	if len(include) == 0 {
		return true, nil
	}
	for _, name := range []string{g.CliName(), g.GqlName()} {
		for _, pattern := range include {
			if match, err := fnPathContains(pattern, name); err != nil {
				return false, err
			} else if match {
				return true, nil
			}
			if match, err := fnPathGlob(pattern, name); err != nil {
				return false, err
			} else if match {
				return true, nil
			}
			pattern = strings.ReplaceAll(pattern, ":", "/")
			name = strings.ReplaceAll(name, "", "/")
			if matched, err := doublestar.PathMatch(pattern, name); err != nil {
				return false, err
			} else if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

func (gg *GeneratorGroup) List(ctx context.Context) ([]*Generator, error) {
	return gg.Generators, nil
}

// Run all the generators in the group
func (gg *GeneratorGroup) Run(ctx context.Context) (*GeneratorGroup, error) {
	gg = gg.Clone()

	dag, err := dagForCheck(ctx, gg.Module)
	if err != nil {
		return nil, err
	}
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	eg := new(errgroup.Group)
	for _, generator := range gg.Generators {
		ctx, span := Tracer(ctx).Start(ctx, generator.Name(),
			telemetry.Reveal(),
			trace.WithAttributes(
				attribute.Bool(telemetry.UIRollUpLogsAttr, true),
				attribute.Bool(telemetry.UIRollUpSpansAttr, true),
				attribute.String(telemetry.GeneratorNameAttr, generator.Name()),
			),
		)
		generator.Completed = false
		generator.Changes = nil
		eg.Go(func() (rerr error) {
			defer func() {
				generator.Completed = true
				// span.SetAttributes(attribute.Bool(telemetry.GeneratorPassedAttr, generator.Passed))
				telemetry.EndWithCause(span, &rerr)
			}()
			cs, err := generator.run(ctx, dag, clientMD.EnableCloudScaleOut)
			if err != nil {
				return err
			}
			generator.Changes = cs
			return nil
		})
	}
	_ = eg.Wait()
	return gg, nil
}

func (gg *GeneratorGroup) Changes(ctx context.Context, continueOnConflicts bool) (*Changeset, error) {
	var cs *Changeset
	var err error
	for i, g := range gg.Generators {
		if i == 0 {
			cs = g.Changes
			continue
		}
		cs, err = cs.WithChangeset(ctx, g.Changes, continueOnConflicts)
		if err != nil {
			return nil, err
		}
	}
	return cs, nil
}

func (gg *GeneratorGroup) Clone() *GeneratorGroup {
	c := *gg
	for i := range c.Generators {
		c.Generators[i] = c.Generators[i].Clone()
	}
	c.Module = gg.Module.Clone()
	return &c
}

func (g *Generator) Clone() *Generator {
	c := *g
	c.Module = g.Module.Clone()
	return &c
}

func (g *Generator) Name() string {
	return strings.Join(g.Path, ":")
}

func (g *Generator) Query() []dagql.Selector {
	var q []dagql.Selector
	for _, field := range g.Path {
		q = append(q, dagql.Selector{Field: gqlFieldName(field)})
	}
	return q
}

func (g *Generator) Run(ctx context.Context) (*Generator, error) {
	g = g.Clone()

	dag, err := dagForCheck(ctx, g.Module)
	if err != nil {
		return nil, err
	}
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	var span trace.Span
	if clientMD.CloudScaleOutEngineID != "" {
		ctx, span = Tracer(ctx).Start(ctx, g.Name(),
			telemetry.Reveal(),
			trace.WithAttributes(
				attribute.Bool(telemetry.UIRollUpLogsAttr, true),
				attribute.Bool(telemetry.UIRollUpSpansAttr, true),
				attribute.String(telemetry.GeneratorNameAttr, g.Name()),
			),
		)
	}

	g.Completed = false
	g.Changes = nil

	var generatorErr error
	defer func() {
		g.Completed = true

		if span != nil {
			// span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, ))
			telemetry.EndWithCause(span, &generatorErr)
		}
	}()
	g.Changes, generatorErr = g.run(ctx, dag, false)
	return g, nil
}

func (g *Generator) run(
	ctx context.Context,
	dag *dagql.Server,
	tryScaleOut bool,
) (_ *Changeset, rerr error) {
	if tryScaleOut {
		return nil, fmt.Errorf("not implemented")
	}

	selectPath := []dagql.Selector{{Field: gqlFieldName(g.Module.Name())}}
	for _, field := range g.Path {
		selectPath = append(selectPath, dagql.Selector{Field: field})
	}

	var generatorParent dagql.AnyObjectResult
	if err := (func(ctx context.Context) (rerr error) {
		ctx, span := Tracer(ctx).Start(ctx, "load generator context",
			// Prevent logs from bubbling up past this point.
			telemetry.Boundary(),
			// We're only using this span as a log encapsulation boundary; show
			// its child spans inline.
			telemetry.Passthrough(),
		)
		defer telemetry.EndWithCause(span, &rerr)
		return dag.Select(ctx, dag.Root(), &generatorParent, selectPath[:len(selectPath)-1]...)
	})(ctx); err != nil {
		return nil, err
	}

	// Call the generator
	var changes dagql.ObjectResult[*Changeset]
	if err := dag.Select(dagql.WithNonInternalTelemetry(ctx), generatorParent, &changes, selectPath[len(selectPath)-1]); err != nil {
		return nil, err
	}

	return changes.Self(), nil
}
