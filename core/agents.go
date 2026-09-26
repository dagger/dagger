package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/engine/slog"
)

func (llm *LLM) WarnToolNameCollisions(ctx context.Context) {
	if llm == nil || llm.mcp == nil {
		return
	}
	collisions, err := llm.mcp.ToolNameCollisions(ctx)
	if err != nil || len(collisions) == 0 {
		return
	}
	logger := slog.SpanLogger(ctx, InstrumentationLibrary)
	for name, contributors := range collisions {
		served := make([]string, len(contributors))
		for i, typeName := range contributors {
			served[i] = namespacedToolName(typeName, name)
		}
		logger.Warn("agent tool name collision: serving the conflicting toolsets under namespaced names",
			"tool", name,
			"contributors", contributors,
			"served", served)
	}
}

// Expertise retains a callable @agent field, including its source workspace.
// It does not evaluate the function until it receives a base conversation.
type Expertise struct {
	Artifact *Artifact
}

func (*Expertise) Type() *ast.Type {
	return &ast.Type{NamedType: "Expertise", NonNull: true}
}

func (*Expertise) TypeDescription() string {
	return "An agent function that can modify a conversation."
}

func NewExpertise(artifact *Artifact) (*Expertise, error) {
	if artifact.TypeName != "Expertise" || !slices.Contains(artifact.Directives, "agent") || artifact.Node == nil || artifact.Node.OriginalModule.Self() == nil {
		uri, err := artifact.URI(ArtifactURIOpts{DimensionKeys: true})
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s is not a source of expertise", uri)
	}
	return &Expertise{Artifact: artifact.Clone()}, nil
}

func (a *Expertise) Clone() *Expertise {
	return &Expertise{Artifact: a.Artifact.Clone()}
}

func (a *Expertise) Name() string            { return a.Artifact.Node.CommandName() }
func (a *Expertise) Description() string     { return a.Artifact.Node.Description }
func (a *Expertise) Path() []string          { return a.Artifact.Node.Path() }
func (a *Expertise) OriginalModule() *Module { return a.Artifact.Node.OriginalModule.Self() }

func (a *Expertise) Run(ctx context.Context, base dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	var result dagql.ObjectResult[*LLM]
	ctx, err := WorkspaceClientContext(ctx, a.Artifact.Workspace.Self())
	if err != nil {
		return result, err
	}
	ctx = WorkspaceToContext(ctx, a.Artifact.Workspace)
	id, err := base.ID()
	if err != nil {
		return result, err
	}
	node := a.Artifact.Node
	if node.Parent != nil {
		if obj := node.Parent.ObjectType(); obj != nil {
			if fn, ok := obj.FunctionByName(node.Name); ok {
				for _, arg := range fn.Args {
					typ := arg.Self().TypeDef.Self()
					if typ.Kind == TypeDefKindObject && typ.AsObject.Valid && typ.AsObject.Value.Self().Name == "LLM" {
						err := a.Artifact.Evaluate(ctx, &result, dagql.NamedInput{Name: arg.Self().Name, Value: dagql.NewID[*LLM](id)})
						return result, err
					}
				}
			}
		}
	}
	return result, fmt.Errorf("agent %q has no LLM argument", a.Name())
}

func (a *Expertise) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return a.Artifact.EncodePersistedObject(ctx, enc)
}
func (*Expertise) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, raw json.RawMessage) (dagql.Typed, error) {
	artifact, err := (*Artifact)(nil).DecodePersistedObject(ctx, dec, raw)
	if err != nil {
		return nil, err
	}
	return &Expertise{Artifact: artifact.(*Artifact)}, nil
}
func (a *Expertise) AttachDependencyResults(ctx context.Context, owner dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return a.Artifact.AttachDependencyResults(ctx, owner, attach)
}

// ComposeExpertise passes the conversation through the expertise in list order.
// Existing contributions are retained; use RecomposeExpertise to replace them.
func ComposeExpertise(ctx context.Context, base dagql.ObjectResult[*LLM], expertise []*Expertise) (dagql.ObjectResult[*LLM], error) {
	acc := base
	for _, entry := range expertise {
		next, err := entry.Run(ctx, acc)
		if err != nil {
			return base, fmt.Errorf("compose agent %q: %w", entry.Name(), err)
		}
		acc = next
	}
	acc.Self().WarnToolNameCollisions(ctx)
	return acc, nil
}
