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
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

const artifactViewField = "__withArtifactView"

// Stored fields belong to the source object. A view wraps that object but does
// not publish a second tree of its inline fields. Derive their SDK call frames
// from the source, then apply the active view to each stored reference.
func artifactViewSourceCall(ctx context.Context, frame *dagql.ResultCall) (*dagql.ResultCall, error) {
	for frame != nil && frame.Field == artifactViewField && frame.Receiver != nil {
		if frame.Receiver.Call != nil {
			frame = frame.Receiver.Call
			continue
		}
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		frame, err = cache.ResultCallByResultID(ctx, "", frame.Receiver.ResultID)
		if err != nil {
			return nil, err
		}
	}
	return frame, nil
}

type artifactViewRule struct {
	Dimension  string
	Type       string
	Keys       []string
	UniqueType bool
}

type artifactViewState struct {
	Rules []artifactViewRule
	Paths []artifactViewPath
	// Producer origins ignore view wrappers, so selections survive scoped calls.
	Selections map[string][]string
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
	CollectionType string
	Dimension      string
	Root           string
	Fields         []artifactViewPathField
	Route          []string `json:",omitempty"`
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
		path := artifactViewPath{Dimension: root.Node.CollectionDimension.Identifier, CollectionType: root.Node.CollectionDimension.CollectionType, Route: root.Node.CollectionRoute}
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
				name = node.Module.Self().ConstructorName()
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

// ApplyToID records the view as a deferred field selection. Resolving the
// module schema is allowed; evaluating the referenced object's producer is not.
func (v *ArtifactView) ApplyToID(ctx context.Context, srv *dagql.Server, id *call.ID) (*call.ID, error) {
	if id == nil {
		return nil, fmt.Errorf("cannot scope a nil object ID")
	}
	if id.IsHandle() {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		metadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, err
		}
		frame, err := cache.ResultCallByResultID(ctx, metadata.SessionID, id.EngineResultID())
		if err != nil {
			return nil, err
		}
		id, err = frame.RecipeID(ctx)
		if err != nil {
			return nil, err
		}
	}
	class, _, found, err := srv.ObjectTypeAndServerForID(dagql.WithResultScope(ctx, nil), id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("cannot resolve lazy object type %s", id.Type().NamedType())
	}
	if _, ok := class.Typed().(*ModuleObject); !ok {
		return id, nil
	}
	// Convert only already-attached view sources to recipes. This keeps saved
	// lazy bindings independent of this engine's result handles.
	sources := make([]any, len(v.sources))
	for i, source := range v.sources {
		recipe, err := source.RecipeID(ctx)
		if err != nil {
			return nil, err
		}
		sources[i] = recipe
	}
	state, err := json.Marshal(v.state)
	if err != nil {
		return nil, err
	}
	input, err := (dagql.InputObject[artifactViewInput]{}).Decoder().DecodeInput(map[string]any{"sources": sources, "state": string(state)})
	if err != nil {
		return nil, err
	}
	wrap := func(source *call.ID) *call.ID {
		return source.Append(id.Type().ToAST(), artifactViewField,
			call.WithModule(id.Module()),
			call.WithArgs(call.NewArgument("view", input.ToLiteral(), false)),
			call.WithImplicitInputs(call.NewArgument("artifactViewType", call.NewLiteralString(class.TypeName()), false)))
	}
	if id.Field() == artifactViewField && wrap(id.Receiver()).Digest() == id.Digest() {
		return id, nil
	}
	return wrap(id), nil
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
			// Views carry concrete installed SDK types. Equal source content
			// under another installation name cannot reuse this result.
			GetDynamicInput: func(ctx context.Context, _ dagql.AnyResult, _ map[string]dagql.Input, _ call.View, req *dagql.CallRequest) error {
				return req.SetImplicitInput(ctx, "artifactViewType", dagql.String(obj.TypeDef.Name))
			},
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
			return applyModuleObjectView(ctx, self, view)
		},
	}, nil
}

// A base records collection ancestry. Subsets and author state changes retain
// it, so aliases must also have the same current fields.
func sameArtifactViewCollection(left, right dagql.AnyResult) (bool, error) {
	a, ok := dagql.UnwrapAs[*ModuleObject](left)
	if !ok || a.TypeDef.Collection == nil || !a.TypeDef.Collection.Enabled {
		return false, nil
	}
	b, ok := dagql.UnwrapAs[*ModuleObject](right)
	if !ok || a.TypeDef.Name != b.TypeDef.Name {
		return false, nil
	}
	leftBase, rightBase := left, right
	if a.CollectionBase != nil {
		leftBase = a.CollectionBase
	}
	if b.CollectionBase != nil {
		rightBase = b.CollectionBase
	}
	leftID, err := leftBase.ID()
	if err != nil {
		return false, err
	}
	rightID, err := rightBase.ID()
	if err != nil {
		return false, err
	}
	if leftID.EngineResultID() != rightID.EngineResultID() {
		return false, nil
	}
	changed, err := changedStateFields(a.Fields, b.Fields)
	if err != nil {
		return false, err
	}
	members, err := a.TypeDef.CollectionMembers()
	if err != nil {
		return false, err
	}
	for _, field := range changed {
		// The engine fills delta metadata during SDK conversion.
		if members.Delta == nil || field != members.Delta.OriginalName {
			return false, nil
		}
	}
	return true, nil
}

func (v *ArtifactView) collectionSelection(ctx context.Context, result dagql.AnyResult) ([]string, bool, error) {
	if len(v.state.Selections) == 0 {
		return nil, false, nil
	}
	recipe, err := result.RecipeID(ctx)
	if err != nil {
		return nil, false, err
	}
	keys, found := v.state.Selections[artifactViewOrigin(recipe)]
	return keys, found, nil
}

func applyModuleObjectView(ctx context.Context, self dagql.ObjectResult[*ModuleObject], view *ArtifactView) (dagql.AnyResult, error) {
	copy := *self.Self()
	copy.Fields = maps.Clone(self.Self().Fields)
	copy.ArtifactView = view
	if copy.TypeDef.Collection != nil && copy.TypeDef.Collection.Enabled {
		selected, bound, err := view.collectionSelection(ctx, self)
		if err != nil {
			return nil, err
		}
		var route []string
		if !bound {
			recipe, err := self.RecipeID(ctx)
			if err != nil {
				return nil, err
			}
			for _, path := range view.state.Paths {
				if !path.matches(recipe) {
					continue
				}
				// Known unfiltered paths must not use another dimension's rule.
				for _, rule := range view.state.Rules {
					if rule.Dimension == path.Dimension || rule.Dimension == path.CollectionType {
						selected, route, bound = rule.Keys, path.Route, true
						break
					}
				}
				if !bound {
					return dagql.NewResultForCurrentCall(ctx, &copy)
				}
				break
			}
		}
		// A value's own selection takes precedence over the keys kept on its
		// base to let module code construct aliases and derived collections.
		if !bound && copy.CollectionBase != nil {
			selected, bound, err = view.collectionSelection(ctx, copy.CollectionBase)
			if err != nil {
				return nil, err
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
			keys = slices.DeleteFunc(keys, func(key collectionKey) bool { return !slices.Contains(selected, flattening.RoutedKey(route, key.text)) })
			subset, err := copy.collectionSubset(ctx, keys)
			if err != nil {
				return nil, err
			}
			copy = *subset
		}
	}
	return dagql.NewResultForCurrentCall(ctx, &copy)
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
	rules     []artifactViewRule
	sources   []*artifactViewSource
	originals map[string]artifactCollectionSnapshot
}

type artifactCollectionSnapshot struct {
	source *artifactViewSource
	keys   []string
}

// A source can supply both a selected value and the base needed to construct
// other values. Only aliases with the same current state share selected keys.
type artifactViewSource struct {
	result   dagql.AnyObjectResult
	origins  []string
	selected map[string][]string
	required map[string][]string
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
	if len(original.source.selected) > 0 {
		selected := artifactViewSelectionKeys(original.source.selected)
		expected = slices.DeleteFunc(slices.Clone(original.keys), func(key string) bool {
			return !slices.Contains(selected, key)
		})
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

// record selects the keys of node's collection value by their coordinates on
// node's route. One value can be on several routes of one rule: it keeps the
// keys that any of them selects. Different rules for one value intersect.
func (d *artifactViewDiscovery) record(ctx context.Context, receiver *Artifact, result dagql.AnyObjectResult, node *ModTreeNode, keys []collectionKey) error {
	var selected []string
	var dimension string
	filtered := false
	for _, rule := range d.rules {
		if dimensionMatches(node.CollectionDimension, rule.Dimension) {
			filtered, dimension = true, rule.Dimension
			for _, key := range keys {
				if slices.Contains(rule.Keys, node.coordinateKey(key.text)) {
					selected = append(selected, key.text)
				}
			}
			break
		}
	}
	recipe, err := result.RecipeID(ctx)
	if err != nil {
		return err
	}
	var base dagql.AnyObjectResult
	if obj, ok := dagql.UnwrapAs[*ModuleObject](result); ok && obj.CollectionBase != nil {
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return err
		}
		base, err = srv.ToSelectable(dagql.WithResultScope(ctx, nil), obj.CollectionBase)
		if err != nil {
			return err
		}
		same, err := sameArtifactViewCollection(result, base)
		if err != nil {
			return err
		}
		if same {
			result = base
		}
	}
	identity, err := artifactCollectionIdentity(receiver)
	if err != nil {
		return err
	}
	original := artifactCollectionSnapshot{keys: make([]string, len(keys))}
	for i, key := range keys {
		original.keys[i] = key.text
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	original.source, err = d.recordSource(ctx, result)
	if err != nil {
		return err
	}
	if filtered {
		artifactViewAddSelection(original.source.selected, dimension, selected)
		artifactViewAddSelection(original.source.required, dimension, selected)
		original.source.origins = append(original.source.origins, artifactViewOrigin(recipe))
		if base != nil {
			// Base keys support evaluation; they must not widen the value's selection.
			dependency, err := d.recordSource(ctx, base)
			if err != nil {
				return err
			}
			artifactViewAddSelection(dependency.required, dimension, selected)
		}
	}
	d.originals[identity] = original
	return nil
}

// buildView freezes discovery into one lookup table. Base requirements are
// defaults; each producer's own selection takes precedence.
func (d *artifactViewDiscovery) buildView(ctx context.Context, roots []*Artifact) (*ArtifactView, error) {
	view := &ArtifactView{state: artifactViewState{Rules: d.rules, Selections: map[string][]string{}}}
	if err := view.addPaths(ctx, roots); err != nil {
		return nil, err
	}
	// Sort references by recipe so discovery order and local handles do not
	// change the view's cache identity.
	type sourceKey struct {
		result dagql.AnyObjectResult
		recipe string
	}
	var order []sourceKey
	for _, source := range d.sources {
		if len(source.required) == 0 {
			continue
		}
		recipe, err := source.result.RecipeID(ctx)
		if err != nil {
			return nil, err
		}
		encoded, err := recipe.Encode()
		if err != nil {
			return nil, err
		}
		order = append(order, sourceKey{source.result, encoded})
		view.state.Selections[artifactViewOrigin(recipe)] = artifactViewSelectionKeys(source.required)
	}
	slices.SortFunc(order, func(a, b sourceKey) int { return strings.Compare(a.recipe, b.recipe) })
	for _, source := range order {
		view.sources = append(view.sources, source.result)
	}
	for _, source := range d.sources {
		keys := artifactViewSelectionKeys(source.selected)
		for _, origin := range source.origins {
			view.state.Selections[origin] = keys
		}
	}
	if err := view.setKey(); err != nil {
		return nil, err
	}
	return view, nil
}

func artifactViewAddSelection(rules map[string][]string, dimension string, selected []string) {
	keys := append(slices.Clone(rules[dimension]), selected...)
	slices.Sort(keys)
	rules[dimension] = slices.Compact(keys)
}

func artifactViewSelectionKeys(rules map[string][]string) []string {
	var keys []string
	for i, dimension := range slices.Sorted(maps.Keys(rules)) {
		if i == 0 {
			keys = slices.Clone(rules[dimension])
		} else {
			keys = slices.DeleteFunc(keys, func(key string) bool {
				return !slices.Contains(rules[dimension], key)
			})
		}
	}
	return keys
}

// recordSource groups aliases by their current state. The caller holds d.mu.
func (d *artifactViewDiscovery) recordSource(ctx context.Context, result dagql.AnyObjectResult) (*artifactViewSource, error) {
	for _, source := range d.sources {
		same, err := sameArtifactViewCollection(source.result, result)
		if err != nil {
			return nil, err
		}
		if same {
			// Choose the same representative regardless of discovery order.
			current, err := source.result.RecipeID(ctx)
			if err != nil {
				return nil, err
			}
			candidate, err := result.RecipeID(ctx)
			if err != nil {
				return nil, err
			}
			currentRecipe, err := current.Encode()
			if err != nil {
				return nil, err
			}
			candidateRecipe, err := candidate.Encode()
			if err != nil {
				return nil, err
			}
			if candidateRecipe < currentRecipe {
				source.result = result
			}
			return source, nil
		}
	}
	source := &artifactViewSource{result: result, selected: map[string][]string{}, required: map[string][]string{}}
	d.sources = append(d.sources, source)
	return source, nil
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
			if dimensionMatches(dimension, filter.Dimension) {
				keys := slices.Clone(filter.Keys)
				slices.Sort(keys)
				sameType := 0
				for _, candidate := range a.AllDimensions {
					if candidate.CollectionType == dimension.CollectionType {
						sameType++
					}
				}
				d.rules = append(d.rules, artifactViewRule{
					Dimension: filter.Dimension, Type: dimension.CollectionType,
					Keys: slices.Compact(keys), UniqueType: filter.Dimension == dimension.CollectionType || sameType == 1,
				})
				break
			}
		}
	}
	slices.SortFunc(d.rules, func(a, b artifactViewRule) int {
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
