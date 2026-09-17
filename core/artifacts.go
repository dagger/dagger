package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

// ArtifactCollectionKey selects an item from a named collection.
type ArtifactCollectionKey struct {
	Collection string `field:"true" doc:"The collection identifier, fixed across the workspace schema."`
	Key        string `field:"true" doc:"The collection item's key."`
}

func (*ArtifactCollectionKey) Type() *ast.Type {
	return &ast.Type{NamedType: "ArtifactCollectionKey", NonNull: true}
}

// Artifact holds a complete address and its deferred object value. The module
// tree and workspace are retained so evaluation does not depend on the caller.
type Artifact struct {
	Query          []string                 `field:"true" doc:"Ordered, literal fields to follow. Entrypoint targets use their shorthand."`
	CollectionKeys []*ArtifactCollectionKey `field:"true" doc:"One key per collection along the query. Unordered; empty for static artifacts."`
	TypeName       string
	Node           *ModTreeNode
	Workspace      dagql.ObjectResult[*Workspace]
}

// Clone gives each API result its own writable dependency wrappers. Attachment
// rewrites those wrappers, including the module tree's parent chain.
func (a *Artifact) Clone() *Artifact {
	copy := *a
	parent := &copy.Node
	for node := a.Node; node != nil; node = node.Parent {
		cloned := *node
		*parent = &cloned
		parent = &cloned.Parent
	}
	return &copy
}

func (*Artifact) Type() *ast.Type { return &ast.Type{NamedType: "Artifact", NonNull: true} }
func (*Artifact) TypeDescription() string {
	return "One workspace value with a complete query and all required collection keys. Reading metadata does not evaluate the value. Different addresses remain distinct even if they return the same object."
}

// Artifacts is an immutable selection. Filtering changes only the entry list,
// preserving each artifact's address, module tree, and workspace.
type Artifacts struct{ Entries []*Artifact }

var _ dagql.PersistedObject = (*Artifact)(nil)
var _ dagql.PersistedObjectDecoder = (*Artifact)(nil)
var _ dagql.HasDependencyResults = (*Artifact)(nil)
var _ dagql.PersistedObject = (*Artifacts)(nil)
var _ dagql.PersistedObjectDecoder = (*Artifacts)(nil)
var _ dagql.HasDependencyResults = (*Artifacts)(nil)

func (*Artifacts) Type() *ast.Type { return &ast.Type{NamedType: "Artifacts", NonNull: true} }
func (*Artifacts) TypeDescription() string {
	return "An immutable selection of workspace artifacts. Listed types, collections, and keys use OR; chained filters use AND. Empty alternatives and unknown names match nothing. Filters never change addresses or collection identifiers."
}

func (a *Artifacts) filter(matches func(*Artifact) bool) *Artifacts {
	selected := &Artifacts{Entries: make([]*Artifact, 0, len(a.Entries))}
	for _, artifact := range a.Entries {
		if matches(artifact) {
			selected.Entries = append(selected.Entries, artifact.Clone())
		}
	}
	return selected
}
func (a *Artifacts) FilterTypes(types []string) *Artifacts {
	return a.filter(func(artifact *Artifact) bool { return slices.Contains(types, artifact.TypeName) })
}
func (a *Artifacts) FilterQuery(query []string) *Artifacts {
	return a.filter(func(artifact *Artifact) bool { return slices.Equal(query, artifact.Query) })
}
func (a *Artifacts) FilterCollections(collections []string) *Artifacts {
	return a.filter(func(artifact *Artifact) bool {
		for _, key := range artifact.CollectionKeys {
			if slices.Contains(collections, key.Collection) {
				return true
			}
		}
		return false
	})
}
func (a *Artifacts) FilterCollectionKeys(collection string, keys []string) *Artifacts {
	return a.filter(func(artifact *Artifact) bool {
		for _, key := range artifact.CollectionKeys {
			if key.Collection == collection && slices.Contains(keys, key.Key) {
				return true
			}
		}
		return false
	})
}
func (a *Artifacts) Collections() []string {
	names := map[string]struct{}{}
	for _, artifact := range a.Entries {
		for _, key := range artifact.CollectionKeys {
			names[key.Collection] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(names))
}
func (a *Artifacts) CollectionKeys(collection string) []string {
	keys := map[string]struct{}{}
	for _, artifact := range a.Entries {
		for _, key := range artifact.CollectionKeys {
			if key.Collection == collection {
				keys[key.Key] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(keys))
}
func (a *Artifacts) One() (*Artifact, error) {
	if len(a.Entries) != 1 {
		return nil, fmt.Errorf("expected exactly one artifact, found %d", len(a.Entries))
	}
	return a.Entries[0].Clone(), nil
}

// Static artifacts have no collection flags. Their literal field names are
// already in CLI case and can be joined without interpreting user input.
func (a *Artifact) Pretty() string {
	return strings.Join(a.Query, ":")
}
func (a *Artifacts) Pretty() []string {
	lines := make([]string, 0, len(a.Entries))
	for _, artifact := range a.Entries {
		lines = append(lines, artifact.Pretty())
	}
	return lines
}

// ModuleArtifactNodes walks metadata only. Module objects are namespaces; the
// first non-null core object on each path is an artifact. Caller arguments,
// nullable values, and lists cannot be resolved through this no-argument API.
func ModuleArtifactNodes(ctx context.Context, mod dagql.ObjectResult[*Module]) (*ModTreeNode, []*ModTreeNode, error) {
	root, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, nil, err
	}
	var nodes []*ModTreeNode
	seen := map[string]bool{}
	err = root.Walk(ctx, func(_ context.Context, node *ModTreeNode) (bool, error) {
		typ := node.Type.Self()
		if typ == nil || typ.Optional || typ.Kind != TypeDefKindObject {
			return false, nil
		}
		var fn *Function
		if node.Parent == nil {
			if obj := node.ObjectType(); obj != nil && obj.Constructor.Valid {
				fn = obj.Constructor.Value.Self()
			}
		} else if obj := node.Parent.ObjectType(); obj != nil {
			fn, _ = obj.FunctionByName(node.Name)
		}
		if fn != nil {
			if fn.ReturnType.Self().Optional {
				return false, nil
			}
			for _, arg := range fn.Args {
				if argRequired(arg.Self()) {
					return false, nil
				}
			}
		}
		obj := node.ObjectType()
		if obj == nil {
			return false, nil
		}
		if fullType, ok := node.OriginalModule.Self().objectTypeDefResultByName(obj.Name); ok {
			// Field types can be references with no members. ModTree already
			// adds a separate complete subtree for function return types.
			if fn == nil {
				node.Type = fullType
			}
			return true, nil
		}
		if obj.SourceModuleName != "" {
			return true, nil
		}
		if node == root {
			return true, nil
		}
		path := node.PathString()
		if !seen[path] {
			seen[path] = true
			nodes = append(nodes, node)
		}
		return false, nil
	})
	return root, nodes, err
}

// Persist both individual artifacts and selections. One tree encoding shares
// module and type references across all entries in a selection.
type persistedArtifact struct {
	Query          []string
	CollectionKeys []*ArtifactCollectionKey
	TypeName       string
	Node           int
	Workspace      uint64
}
type persistedArtifacts struct {
	Tree    persistedModTree
	Entries []persistedArtifact
}

func encodeArtifacts(cache dagql.PersistedObjectCache, entries []*Artifact) (dagql.PersistedObjectEncoding, error) {
	tree := newPersistedModTreeEncoder(cache)
	payload := persistedArtifacts{}
	for _, a := range entries {
		p := persistedArtifact{Query: a.Query, CollectionKeys: a.CollectionKeys, TypeName: a.TypeName}
		var err error
		p.Node, err = tree.Add(a.Node)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		if a.Workspace.Self() != nil {
			p.Workspace, err = encodePersistedObjectRef(cache, a.Workspace, "artifact workspace")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
		}
		payload.Entries = append(payload.Entries, p)
	}
	payload.Tree = tree.tree
	return encodePersistedObjectPayload(payload)
}
func decodeArtifacts(ctx context.Context, srv *dagql.Server, raw json.RawMessage) (*Artifacts, error) {
	var payload persistedArtifacts
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	nodes, err := decodePersistedModTree(ctx, srv, payload.Tree)
	if err != nil {
		return nil, err
	}
	result := &Artifacts{}
	for _, p := range payload.Entries {
		a := &Artifact{Query: p.Query, CollectionKeys: p.CollectionKeys, TypeName: p.TypeName, Node: nodes[p.Node]}
		if a.Node == nil {
			return nil, fmt.Errorf("artifact references missing tree node %d", p.Node)
		}
		a.Workspace, err = loadPersistedObjectResultByResultID[*Workspace](ctx, srv, p.Workspace, "artifact workspace")
		if err != nil {
			return nil, err
		}
		result.Entries = append(result.Entries, a)
	}
	return result, nil
}
func (a *Artifact) EncodePersistedObject(_ context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	return encodeArtifacts(cache, []*Artifact{a})
}
func (*Artifact) DecodePersistedObject(ctx context.Context, srv *dagql.Server, _ uint64, _ *dagql.ResultCall, raw json.RawMessage) (dagql.Typed, error) {
	result, err := decodeArtifacts(ctx, srv, raw)
	if err != nil {
		return nil, err
	}
	if len(result.Entries) != 1 {
		return nil, fmt.Errorf("invalid persisted artifact count: %d", len(result.Entries))
	}
	return result.Entries[0], nil
}
func (a *Artifacts) EncodePersistedObject(_ context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	return encodeArtifacts(cache, a.Entries)
}
func (*Artifacts) DecodePersistedObject(ctx context.Context, srv *dagql.Server, _ uint64, _ *dagql.ResultCall, raw json.RawMessage) (dagql.Typed, error) {
	return decodeArtifacts(ctx, srv, raw)
}
func (a *Artifact) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	owned, err := attachModTreeNodeDependencyResults(a.Node, attach)
	if err != nil {
		return nil, err
	}
	if a.Workspace.Self() != nil {
		value, err := attach(a.Workspace)
		if err != nil {
			return nil, err
		}
		var ok bool
		a.Workspace, ok = value.(dagql.ObjectResult[*Workspace])
		if !ok {
			return nil, fmt.Errorf("artifact workspace has unexpected type %T", value)
		}
		owned = append(owned, value)
	}
	return owned, nil
}
func (a *Artifacts) AttachDependencyResults(ctx context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	var owned []dagql.AnyResult
	for _, entry := range a.Entries {
		deps, err := entry.AttachDependencyResults(ctx, nil, attach)
		if err != nil {
			return nil, err
		}
		owned = append(owned, deps...)
	}
	return owned, nil
}
