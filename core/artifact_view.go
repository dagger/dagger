package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

const artifactViewField = "__withArtifactView"

type artifactViewRule struct {
	Dimension  string
	Type       string
	Keys       []string
	UniqueType bool
}

type artifactViewState struct {
	Rules   []artifactViewRule
	Keys    [][]string
	Origins []string
	Paths   []artifactViewPath
}

// Remove view wrappers from call inputs to match a collection's source across
// scoped execution. Keep arguments: different calls can create different keys.
func artifactViewOrigin(id *call.ID) string {
	memo := map[string]*call.ID{}
	var origin func(*call.ID) *call.ID
	var literal func(call.Literal) call.Literal
	args := func(values []*call.Argument) []*call.Argument {
		result := make([]*call.Argument, len(values))
		for i, value := range values {
			result[i] = value.WithValue(literal(value.Value()))
		}
		return result
	}
	literal = func(value call.Literal) call.Literal {
		switch value := value.(type) {
		case *call.LiteralID:
			return call.NewLiteralID(origin(value.Value()))
		case *call.LiteralList:
			values := make([]call.Literal, value.Len())
			for i, item := range value.Values() {
				values[i] = literal(item)
			}
			return call.NewLiteralList(values...)
		case *call.LiteralObject:
			values := make([]*call.Argument, value.Len())
			for i, arg := range value.Args() {
				values[i] = arg.WithValue(literal(arg.Value()))
			}
			return call.NewLiteralObject(values...)
		default:
			return value
		}
	}
	origin = func(id *call.ID) *call.ID {
		if id == nil {
			return nil
		}
		key := string(id.Digest())
		if cached, ok := memo[key]; ok {
			return cached
		}
		receiver := origin(id.Receiver())
		result := receiver
		if id.Field() != artifactViewField {
			result = receiver.Append(id.Type().ToAST(), id.Field(),
				call.WithModule(id.Module()), call.WithView(id.View()), call.WithNth(int(id.Nth())),
				call.WithArgs(args(id.Args())...), call.WithImplicitInputs(args(id.ImplicitInputs())...))
		}
		memo[key] = result
		return result
	}
	return string(origin(id).Digest())
}

// Paths identify collection dimensions across calls on scoped receivers.
// Arguments select instances within a dimension, so they are not part of a path.
type artifactViewPath struct {
	Dimension string
	Root      string
	Fields    []artifactViewPathField
}

type artifactViewPathField struct {
	Name string
	Type string
}

func (p artifactViewPath) matches(id *call.ID) bool {
	for _, field := range p.Fields {
		for id != nil && id.Field() == artifactViewField {
			id = id.Receiver()
		}
		if id == nil || id.Field() != field.Name || id.Type().NamedType() != field.Type {
			return false
		}
		id = id.Receiver()
	}
	for id != nil && id.Field() == artifactViewField {
		id = id.Receiver()
	}
	return artifactViewOrigin(id) == p.Root
}

func (v *ArtifactView) addPaths(ctx context.Context, roots []*Artifact) error {
	for _, root := range roots {
		path := artifactViewPath{Dimension: root.Node.CollectionDimension.Identifier}
		for node := root.Node.Parent; node != nil; node = node.Parent {
			if node.RootValue != nil {
				id, err := node.RootValue.RecipeID(ctx)
				if err != nil {
					return err
				}
				path.Root = artifactViewOrigin(id)
				break
			}
			name := node.Name
			constructor := node.Parent == nil || (node.Parent.Module.Self() == nil && node.Parent.RootValue == nil && node.Parent.Type.Self() == nil)
			if constructor {
				name = gqlFieldName(node.Module.Self().Name())
			}
			path.Fields = append(path.Fields, artifactViewPathField{Name: name, Type: node.Type.Self().ToType().Name()})
			if constructor {
				break
			}
		}
		v.state.Paths = append(v.state.Paths, path)
	}
	slices.SortFunc(v.state.Paths, func(a, b artifactViewPath) int { return strings.Compare(a.Dimension, b.Dimension) })
	return nil
}

type ArtifactView struct {
	state   artifactViewState
	sources []dagql.AnyObjectResult
	key     string
}

type artifactViewInput struct {
	Sources dagql.ArrayInput[dagql.AnyID]
	State   JSON
}

func (artifactViewInput) TypeName() string { return "ArtifactViewInput" }

func (v *ArtifactView) input() (dagql.InputObject[artifactViewInput], error) {
	var zero dagql.InputObject[artifactViewInput]
	sources := make([]any, 0, len(v.sources))
	for _, source := range v.sources {
		id, err := source.ID()
		if err != nil {
			return zero, err
		}
		sources = append(sources, id)
	}
	state, err := json.Marshal(v.state)
	if err != nil {
		return zero, err
	}
	input, err := zero.Decoder().DecodeInput(map[string]any{"sources": sources, "state": string(state)})
	if err != nil {
		return zero, err
	}
	return input.(dagql.InputObject[artifactViewInput]), nil
}

func loadArtifactView(ctx context.Context, srv *dagql.Server, input artifactViewInput) (*ArtifactView, error) {
	v := &ArtifactView{}
	if err := json.Unmarshal(input.State, &v.state); err != nil {
		return nil, err
	}
	for _, source := range input.Sources {
		id, err := source.ID()
		if err != nil {
			return nil, err
		}
		result, err := srv.Load(dagql.WithResultScope(ctx, nil), id)
		if err != nil {
			return nil, err
		}
		v.sources = append(v.sources, result)
	}
	if err := v.setKey(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *ArtifactView) setKey() error {
	ids := make([]uint64, len(v.sources))
	for i, source := range v.sources {
		id, err := source.ID()
		if err != nil {
			return err
		}
		ids[i] = id.EngineResultID()
	}
	key, err := json.Marshal(struct {
		State   artifactViewState
		Sources []uint64
	}{v.state, ids})
	v.key = string(key)
	return err
}

func (v *ArtifactView) Apply(ctx context.Context, srv *dagql.Server, result dagql.AnyObjectResult) (dagql.AnyObjectResult, error) {
	obj, ok := dagql.UnwrapAs[*ModuleObject](result)
	if !ok || obj.ArtifactView != nil && obj.ArtifactView.key == v.key {
		return result, nil
	}
	input, err := v.input()
	if err != nil {
		return nil, err
	}
	var scoped dagql.AnyObjectResult
	err = srv.Select(dagql.WithResultScope(ctx, nil), result, &scoped, dagql.Selector{
		Field: artifactViewField, Args: []dagql.NamedInput{{Name: "view", Value: input}},
	})
	return scoped, err
}

func (obj *ModuleObject) ResultScope() dagql.ResultScope {
	if obj.ArtifactView == nil {
		return nil
	}
	return obj.ArtifactView
}

func (obj *ModuleObject) artifactViewField(srv *dagql.Server) (dagql.Field[*ModuleObject], error) {
	module, provider, err := NewUserMod(obj.Module).FieldModule()
	if err != nil {
		return dagql.Field[*ModuleObject]{}, err
	}
	installed := newInstalledServer(srv)
	return dagql.Field[*ModuleObject]{
		Spec: &dagql.FieldSpec{
			Name: artifactViewField, Type: obj, Module: module, ModuleProvider: provider,
			Args: dagql.NewInputSpecs(dagql.InputSpec{Name: "view", Type: dagql.InputObject[artifactViewInput]{}}),
		},
		Func: func(ctx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
			srv, err := installed.forObject(ctx, self)
			if err != nil {
				return nil, err
			}
			view, err := loadArtifactView(ctx, srv, args["view"].(dagql.InputObject[artifactViewInput]).Value)
			if err != nil {
				return nil, err
			}
			copy := *self.Self()
			copy.Fields = maps.Clone(self.Self().Fields)
			copy.ArtifactView = view
			if copy.TypeDef.Collection != nil && copy.TypeDef.Collection.Enabled {
				id, err := self.ID()
				if err != nil {
					return nil, err
				}
				baseID := id
				if copy.CollectionBase != nil {
					baseID, err = copy.CollectionBase.ID()
					if err != nil {
						return nil, err
					}
				}
				var selected []string
				bound := false
				for i, source := range view.sources {
					sourceID, err := source.ID()
					if err != nil {
						return nil, err
					}
					if sourceID.EngineResultID() == id.EngineResultID() || sourceID.EngineResultID() == baseID.EngineResultID() {
						selected, bound = view.state.Keys[i], true
						break
					}
				}
				if !bound {
					var recipes []*call.ID
					for _, origin := range []dagql.AnyResult{self, copy.CollectionBase} {
						if origin == nil {
							continue
						}
						recipe, err := origin.RecipeID(ctx)
						if err != nil {
							return nil, err
						}
						recipes = append(recipes, recipe)
						if i := slices.Index(view.state.Origins, artifactViewOrigin(recipe)); i >= 0 {
							selected, bound = view.state.Keys[i], true
							break
						}
					}
					for _, path := range view.state.Paths {
						if bound {
							break
						}
						if !slices.ContainsFunc(recipes, path.matches) {
							continue
						}
						// Known unfiltered paths must not use another dimension's rule.
						for _, rule := range view.state.Rules {
							if rule.Dimension == path.Dimension {
								selected, bound = rule.Keys, true
								break
							}
						}
						if !bound {
							return dagql.NewResultForCurrentCall(ctx, &copy)
						}
						break
					}
				}
				if !bound {
					for _, rule := range view.state.Rules {
						if rule.Type != copy.TypeDef.Name {
							continue
						}
						if !rule.UniqueType {
							return nil, fmt.Errorf("cannot map new collection %s to a dimension: its type occurs on multiple paths", copy.TypeDef.Name)
						}
						selected, bound = rule.Keys, true
						break
					}
				}
				if bound {
					keys, err := copy.collectionKeys(ctx)
					if err != nil {
						return nil, err
					}
					keys = slices.DeleteFunc(keys, func(key collectionKey) bool { return !slices.Contains(selected, key.text) })
					subset, err := copy.collectionSubset(ctx, keys)
					if err != nil {
						return nil, err
					}
					copy = *subset
				}
			}
			return dagql.NewResultForCurrentCall(ctx, &copy)
		},
	}, nil
}

// SetCollectionRoots retains discovery paths independently of static filters.
func (a *Artifacts) SetCollectionRoots(entries []*Artifact) {
	a.CollectionRoots = nil
	seen := map[*ModTreeNode]bool{}
	for _, entry := range entries {
		for node := entry.Node; node != nil; node = node.Parent {
			if node.CollectionDimension == nil || seen[node] {
				continue
			}
			seen[node] = true
			root := *entry
			root.Node = node
			a.CollectionRoots = append(a.CollectionRoots, root.Clone())
		}
	}
}

type artifactViewDiscoveryKey struct{}
type artifactViewDiscovery struct {
	mu        sync.Mutex
	view      ArtifactView
	originals map[string]artifactCollectionSnapshot
}

type artifactCollectionSnapshot struct {
	source uint64
	keys   []string
}

func artifactCollectionIdentity(receiver *Artifact) (string, error) {
	path := *receiver
	path.Path = receiver.Node.Path()
	path.DimensionKeys = nil
	for node := receiver.Node; node != nil; node = node.Parent {
		if node.CollectionDimension != nil && node.CollectionKey != nil {
			path.DimensionKeys = append(path.DimensionKeys, &ArtifactDimensionKey{Dimension: node.CollectionDimension.Identifier, Key: *node.CollectionKey})
		}
	}
	return path.identity()
}

func (d *artifactViewDiscovery) validate(receiver *Artifact, keys []collectionKey) error {
	identity, err := artifactCollectionIdentity(receiver)
	if err != nil {
		return err
	}
	original, ok := d.originals[identity]
	if !ok {
		return fmt.Errorf("collection filtering introduced a new path at %s", receiver.Node.PathString())
	}
	expected := original.keys
	for i, source := range d.view.sources {
		id, err := source.ID()
		if err != nil {
			return err
		}
		if id.EngineResultID() == original.source {
			expected = d.view.state.Keys[i]
			break
		}
	}
	actual := make([]string, len(keys))
	for i, key := range keys {
		actual[i] = key.text
	}
	if !slices.Equal(expected, actual) {
		return fmt.Errorf("collection filtering changed retained child keys at %s: expected %q, got %q", receiver.Node.PathString(), expected, actual)
	}
	return nil
}

func (d *artifactViewDiscovery) record(receiver *Artifact, result dagql.AnyObjectResult, dimension *ArtifactDimension, keys []collectionKey) error {
	var selected []string
	filtered := false
	for _, rule := range d.view.state.Rules {
		if rule.Dimension == dimension.Identifier {
			filtered = true
			for _, key := range keys {
				if slices.Contains(rule.Keys, key.text) {
					selected = append(selected, key.text)
				}
			}
			break
		}
	}
	id, err := result.ID()
	if err != nil {
		return err
	}
	identity, err := artifactCollectionIdentity(receiver)
	if err != nil {
		return err
	}
	original := artifactCollectionSnapshot{source: id.EngineResultID(), keys: make([]string, len(keys))}
	for i, key := range keys {
		original.keys[i] = key.text
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.originals[identity] = original
	if !filtered {
		return nil
	}
	for i, source := range d.view.sources {
		sourceID, err := source.ID()
		if err != nil {
			return err
		}
		if sourceID.EngineResultID() == id.EngineResultID() {
			d.view.state.Keys[i] = slices.DeleteFunc(d.view.state.Keys[i], func(key string) bool { return !slices.Contains(selected, key) })
			return nil
		}
	}
	d.view.sources = append(d.view.sources, result)
	d.view.state.Keys = append(d.view.state.Keys, selected)
	return nil
}

func (a *Artifacts) viewDiscovery() (*artifactViewDiscovery, error) {
	bound, err := a.BindDimensions()
	if err != nil {
		return nil, err
	}
	d := &artifactViewDiscovery{originals: map[string]artifactCollectionSnapshot{}}
	for _, filter := range bound.Selector.Dimensions {
		if filter.Keys == nil || artifact.IsStaticDimension(filter.Dimension) {
			continue
		}
		for _, dimension := range a.AllDimensions {
			if dimension.Identifier == filter.Dimension {
				keys := slices.Clone(filter.Keys)
				slices.Sort(keys)
				sameType := 0
				for _, candidate := range a.AllDimensions {
					if candidate.CollectionType == dimension.CollectionType {
						sameType++
					}
				}
				d.view.state.Rules = append(d.view.state.Rules, artifactViewRule{
					Dimension: filter.Dimension, Type: dimension.CollectionType,
					Keys: slices.Compact(keys), UniqueType: sameType == 1,
				})
				break
			}
		}
	}
	slices.SortFunc(d.view.state.Rules, func(a, b artifactViewRule) int {
		return strings.Compare(a.Dimension, b.Dimension)
	})
	return d, nil
}

type persistedArtifactView struct {
	State   artifactViewState
	Sources []uint64
}

func (v *ArtifactView) encode(enc *dagql.PersistEncodeContext) (*persistedArtifactView, error) {
	if v == nil {
		return nil, nil
	}
	p := &persistedArtifactView{State: v.state}
	for _, source := range v.sources {
		id, err := encodePersistedObjectRef(enc, source, "artifact view collection")
		if err != nil {
			return nil, err
		}
		p.Sources = append(p.Sources, id)
	}
	return p, nil
}

func (p *persistedArtifactView) decode(ctx context.Context, dec *dagql.PersistDecodeContext) (*ArtifactView, error) {
	if p == nil {
		return nil, nil
	}
	v := &ArtifactView{state: p.State}
	for _, id := range p.Sources {
		source, err := loadPersistedResultByResultID(ctx, dec, id, "artifact view collection")
		if err != nil {
			return nil, err
		}
		v.sources = append(v.sources, source.(dagql.AnyObjectResult))
	}
	if err := v.setKey(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *ArtifactView) attach(attach func(dagql.AnyResult) (dagql.AnyResult, error)) (*ArtifactView, []dagql.AnyResult, error) {
	if v == nil {
		return nil, nil, nil
	}
	copy := *v
	copy.sources = slices.Clone(v.sources)
	var owned []dagql.AnyResult
	for i, source := range copy.sources {
		result, err := attach(source)
		if err != nil {
			return nil, nil, err
		}
		copy.sources[i] = result.(dagql.AnyObjectResult)
		owned = append(owned, result)
	}
	if err := copy.setKey(); err != nil {
		return nil, nil, err
	}
	return &copy, owned, nil
}
