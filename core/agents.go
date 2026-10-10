package core

import (
	"context"
	"encoding/json"
	"errors"
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
			"tool", name, "contributors", contributors, "served", served)
	}
}

// Expertise retains a callable @agent field and its bound inputs without
// evaluating it. The conversation argument is supplied by composition.
type Expertise struct {
	Artifact  *Artifact
	Arguments JSON
	Inputs    []dagql.AnyObjectResult
}

func (*Expertise) Type() *ast.Type { return &ast.Type{NamedType: "Expertise", NonNull: true} }
func (*Expertise) TypeDescription() string {
	return "An agent function with bound arguments that can modify a conversation."
}

var ErrUnboundExpertise = errors.New("unbound required agent argument")

func NewExpertise(artifact *Artifact, arguments ...JSON) (*Expertise, error) {
	if artifact.TypeName != "Expertise" || !slices.Contains(artifact.Directives, "agent") || artifact.Node == nil || artifact.Node.OriginalModule.Self() == nil {
		uri, err := artifact.URI(ArtifactURIOpts{DimensionKeys: true})
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s is not a source of expertise", uri)
	}
	raw := JSON("{}")
	if len(arguments) > 0 {
		raw = arguments[0]
	}
	inputs, err := ArtifactInputs(artifact, raw)
	if err != nil {
		return nil, err
	}
	bound := map[string]bool{}
	for _, input := range inputs {
		bound[input.Name] = true
	}
	for _, arg := range artifact.Arguments() {
		a := arg.Self()
		if isCoreLLMArg(a) && argRequired(a) {
			if bound[a.Name] {
				return nil, fmt.Errorf("agent conversation argument %q is supplied by compose", a.Name)
			}
			continue
		}
		if argRequired(a) && !bound[a.Name] {
			return nil, fmt.Errorf("agent %q: %w %q", artifact.Node.Name, ErrUnboundExpertise, a.Name)
		}
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return &Expertise{Artifact: artifact.Clone(), Arguments: canonical}, nil
}

func (a *Expertise) Clone() *Expertise {
	return &Expertise{Artifact: a.Artifact.Clone(), Arguments: slices.Clone(a.Arguments), Inputs: slices.Clone(a.Inputs)}
}
func (a *Expertise) Name() string            { return a.Artifact.Node.CommandName() }
func (a *Expertise) Description() string     { return a.Artifact.Node.Description }
func (a *Expertise) Path() []string          { return a.Artifact.Node.Path() }
func (a *Expertise) OriginalModule() *Module { return a.Artifact.Node.OriginalModule.Self() }

// Identity intentionally excludes the implementation and workspace revision.
func (a *Expertise) Identity() (string, error) {
	uri, err := a.Artifact.URI(ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return "", err
	}
	return uri + "\n" + string(a.Arguments), nil
}

func (a *Expertise) Run(ctx context.Context, base dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	var result dagql.ObjectResult[*LLM]
	key, err := a.Identity()
	if err != nil {
		return result, err
	}
	ctx = WithExpertiseOwner(ctx, key)
	ctx, err = a.Artifact.WorkspaceContext(ctx)
	if err != nil {
		return result, err
	}
	id, err := base.ID()
	if err != nil {
		return result, err
	}
	inputs, err := ArtifactInputs(a.Artifact, a.Arguments)
	if err != nil {
		return result, err
	}
	for _, arg := range a.Artifact.Arguments() {
		if isCoreLLMArg(arg.Self()) && argRequired(arg.Self()) {
			inputs = append(inputs, dagql.NamedInput{Name: arg.Self().Name, Value: dagql.NewID[*LLM](id)})
			err = a.Artifact.Evaluate(ctx, &result, inputs...)
			return result, err
		}
	}
	return result, fmt.Errorf("agent %q has no LLM argument", a.Name())
}

type persistedExpertise struct {
	persistedArtifacts
	Arguments JSON
	Inputs    []uint64
}

func (a *Expertise) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	artifact, err := a.Artifact.EncodePersistedObject(ctx, enc)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload := persistedExpertise{Arguments: a.Arguments}
	if err := json.Unmarshal(artifact.JSON, &payload.persistedArtifacts); err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	for _, input := range a.Inputs {
		id, err := encodePersistedObjectRef(enc, input, "expertise argument")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.Inputs = append(payload.Inputs, id)
	}
	return encodePersistedObjectPayload(payload)
}
func (*Expertise) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, raw json.RawMessage) (dagql.Typed, error) {
	var payload persistedExpertise
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if len(payload.Arguments) == 0 {
		payload.Arguments = JSON("{}")
	}
	artifact, err := (*Artifact)(nil).DecodePersistedObject(ctx, dec, raw)
	if err != nil {
		return nil, err
	}
	entry := &Expertise{Artifact: artifact.(*Artifact), Arguments: payload.Arguments}
	for _, id := range payload.Inputs {
		input, err := dec.ResultRef(ctx, id)
		if err != nil {
			return nil, err
		}
		entry.Inputs = append(entry.Inputs, input.(dagql.AnyObjectResult))
	}
	return entry, nil
}
func (a *Expertise) AttachDependencyResults(ctx context.Context, owner dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	owned, err := a.Artifact.AttachDependencyResults(ctx, owner, attach)
	if err != nil {
		return nil, err
	}
	for i, input := range a.Inputs {
		result, err := attach(input)
		if err != nil {
			return nil, err
		}
		a.Inputs[i] = result.(dagql.AnyObjectResult)
		owned = append(owned, result)
	}
	return owned, nil
}

// ComposeExpertise records only the outermost composition. Its nested entries
// are reproduced by rerunning that outer entry, rather than independently.
func ComposeExpertise(ctx context.Context, base dagql.ObjectResult[*LLM], expertise dagql.ObjectResultArray[*Expertise]) (dagql.ObjectResult[*LLM], error) {
	_, nested := ExpertiseOwner(ctx)
	record := slices.Clone(base.Self().Expertise)
	if !nested {
		seen := map[string]bool{}
		for _, entry := range record {
			key, err := entry.Self().Identity()
			if err != nil {
				return base, err
			}
			seen[key] = true
		}
		for _, entry := range expertise {
			key, err := entry.Self().Identity()
			if err != nil {
				return base, err
			}
			if seen[key] {
				return base, fmt.Errorf("expertise %q is already composed", entry.Self().Name())
			}
			seen[key] = true
			record = append(record, entry)
		}
	}
	acc := base
	for _, entry := range expertise {
		next, err := entry.Self().Run(ctx, acc)
		if err != nil {
			return base, fmt.Errorf("compose agent %q: %w", entry.Self().Name(), err)
		}
		acc = next
	}
	if !nested {
		var err error
		acc, err = RecordExpertise(ctx, acc, record)
		if err != nil {
			return base, err
		}
	}
	acc.Self().WarnToolNameCollisions(ctx)
	return acc, nil
}

// RecordExpertise uses a selector so checkpoint/portable replay retains the
// composition record even when the entry returned a separately built LLM.
func RecordExpertise(ctx context.Context, llm dagql.ObjectResult[*LLM], entries dagql.ObjectResultArray[*Expertise]) (dagql.ObjectResult[*LLM], error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return llm, err
	}
	ids := make(dagql.ArrayInput[dagql.ID[*Expertise]], len(entries))
	for i, entry := range entries {
		id, err := entry.ID()
		if err != nil {
			return llm, err
		}
		ids[i] = dagql.NewID[*Expertise](id)
	}
	err = srv.Select(ctx, llm, &llm, dagql.Selector{Field: "__withExpertise", Args: []dagql.NamedInput{{Name: "expertise", Value: ids}}})
	return llm, err
}
