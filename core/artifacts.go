package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

// ArtifactDimensionKey selects an item from a named dimension.
type ArtifactDimensionKey struct {
	Dimension string `field:"true" doc:"The dimension identifier, fixed across the workspace schema."`
	Key       string `field:"true" doc:"The dimension item's key."`
}

func (*ArtifactDimensionKey) Type() *ast.Type {
	return &ast.Type{NamedType: "ArtifactDimensionKey", NonNull: true}
}

// Artifact holds a complete address and its deferred object value. The module
// tree and workspace are retained so evaluation does not depend on the caller.
type Artifact struct {
	Path          []string                `field:"true" doc:"Ordered, literal fields to follow. Entrypoint targets use their shorthand."`
	DimensionKeys []*ArtifactDimensionKey `field:"true" doc:"One key per dimension along the path. Unordered; empty for static artifacts."`
	TypeName      string
	Node          *ModTreeNode
	Workspace     dagql.ObjectResult[*Workspace]
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
	return "One workspace value with a complete path and all required dimension keys. Reading metadata does not evaluate the value. Different addresses remain distinct even if they return the same object."
}

// ArtifactURIOpts selects the parts of an artifact's DAG address.
type ArtifactURIOpts struct {
	Absolute      bool
	DimensionKeys bool
	TypeAssertion bool
}

// URI is the artifact's DAG address. See hack/designs/collections-issue.md,
// section 4.
func (a *Artifact) URI(opts ArtifactURIOpts) (string, error) {
	addr := &dagaddress.Address{HasScheme: true, Path: strings.Join(a.Path, "/")}
	if opts.TypeAssertion {
		addr.Types = []string{ArtifactTypeName(a.TypeName)}
	}
	if opts.DimensionKeys {
		for _, key := range a.DimensionKeys {
			addr.Query = append(addr.Query, dagaddress.Pair{Dimension: key.Dimension, Key: key.Key, HasKey: true})
		}
	}
	if opts.Absolute {
		workspace, commit, err := a.Workspace.Self().GitAddress()
		if err != nil {
			return "", err
		}
		addr.Absolute = true
		addr.Workspace = workspace
		addr.Version = commit
	}
	return addr.String(), nil
}

// ArtifactTypeName is the CLI-case form of a GraphQL type name, as used in a
// DAG address scheme: Container gives "container", ProviderDocs gives
// "provider-docs".
func ArtifactTypeName(typeName string) string {
	return strcase.ToKebab(typeName)
}

// GitAddress is the workspace's Git address as a Go import path, and the commit
// it is pinned to. Only workspaces loaded from a Git ref have one.
func (ws *Workspace) GitAddress() (address, commit string, err error) {
	if ws == nil {
		return "", "", fmt.Errorf("workspace has no Git address")
	}
	ref, ok := ws.SourceGitRef()
	if !ok || ref.Self() == nil {
		return "", "", fmt.Errorf("workspace %s has no Git address", ws.Address)
	}
	// The address is <cloneRef>[/<subdir>]@<version>; the version is what the
	// user requested, and the ref carries the commit it resolved to.
	urls, err := gitutil.ParseCloneURL(ws.Address)
	if err != nil {
		return "", "", fmt.Errorf("parse workspace Git address: %w", err)
	}
	location := urls[0]
	address = workspace.NormalizeGitRemote(location.Remote())
	if address == "" {
		return "", "", fmt.Errorf("workspace %s has no Git address", ws.Address)
	}
	if gitRef := ref.Self().Ref; gitRef != nil && gitutil.IsCommitSHA(gitRef.SHA) {
		commit = gitRef.SHA
	} else if location.Fragment != nil && gitutil.IsCommitSHA(location.Fragment.Ref) {
		commit = location.Fragment.Ref
	} else {
		return "", "", fmt.Errorf("workspace %s is not pinned to a commit", ws.Address)
	}
	return address, commit, nil
}

// ArtifactDimensionFilter keeps artifacts with any listed key in one
// dimension. Nil keys keep any key in the dimension.
type ArtifactDimensionFilter struct {
	Dimension string
	Keys      []string
}

// ArtifactSelector records the filters applied to a selection, so the
// selection can be printed as one DAG address. Nil slices are unfiltered.
type ArtifactSelector struct {
	// Paths lists path patterns; an artifact matches any of them. An empty
	// non-nil list matches nothing.
	Paths []string
	// Types lists GraphQL type names; an artifact matches any of them. An
	// empty non-nil list matches nothing.
	Types []string
	// Dimensions are combined with AND.
	Dimensions []ArtifactDimensionFilter
}

// Artifacts is an immutable selection. Filtering changes only the entry list
// and the recorded selector, preserving each artifact's address, module tree,
// and workspace.
type Artifacts struct {
	Entries  []*Artifact
	Selector ArtifactSelector
}

var _ dagql.PersistedObject = (*Artifact)(nil)
var _ dagql.PersistedObjectDecoder = (*Artifact)(nil)
var _ dagql.HasDependencyResults = (*Artifact)(nil)
var _ dagql.PersistedObject = (*Artifacts)(nil)
var _ dagql.PersistedObjectDecoder = (*Artifacts)(nil)
var _ dagql.HasDependencyResults = (*Artifacts)(nil)

func (*Artifacts) Type() *ast.Type { return &ast.Type{NamedType: "Artifacts", NonNull: true} }
func (*Artifacts) TypeDescription() string {
	return "An immutable selection of workspace artifacts. Listed types, dimensions, and keys use OR; chained filters use AND. Empty alternatives and unknown names match nothing. Filters never change addresses or dimension identifiers."
}

func (a *Artifacts) filter(matches func(*Artifact) bool) *Artifacts {
	selected := &Artifacts{Entries: make([]*Artifact, 0, len(a.Entries)), Selector: a.Selector.clone()}
	for _, artifact := range a.Entries {
		if matches(artifact) {
			selected.Entries = append(selected.Entries, artifact.Clone())
		}
	}
	return selected
}

func (sel ArtifactSelector) clone() ArtifactSelector {
	cloned := ArtifactSelector{
		Paths: slices.Clone(sel.Paths),
		Types: slices.Clone(sel.Types),
	}
	for _, dim := range sel.Dimensions {
		cloned.Dimensions = append(cloned.Dimensions, ArtifactDimensionFilter{Dimension: dim.Dimension, Keys: slices.Clone(dim.Keys)})
	}
	return cloned
}

// exactPaths lists the entries' own paths, as patterns that match only them.
// It is the normal form of chained path filters, whose intersection has no
// general pattern form.
func (a *Artifacts) exactPaths() []string {
	paths := make([]string, 0, len(a.Entries))
	for _, artifact := range a.Entries {
		paths = append(paths, strings.Join(artifact.Path, "/"))
	}
	// Preserve an empty, non-nil list: nil means no path filter.
	slices.Sort(paths)
	return slices.Compact(paths)
}

func (a *Artifacts) FilterTypes(types []string) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool { return slices.Contains(types, artifact.TypeName) })
	if a.Selector.Types == nil {
		selected.Selector.Types = slices.Clone(types)
	} else {
		selected.Selector.Types = slices.DeleteFunc(selected.Selector.Types, func(typ string) bool { return !slices.Contains(types, typ) })
	}
	return selected
}

// FilterTypeNames keeps artifacts whose type has any of the listed CLI-case
// names, as written in a DAG address scheme.
func (a *Artifacts) FilterTypeNames(names []string) *Artifacts {
	var types []string
	for _, artifact := range a.Entries {
		if slices.Contains(names, ArtifactTypeName(artifact.TypeName)) && !slices.Contains(types, artifact.TypeName) {
			types = append(types, artifact.TypeName)
		}
	}
	if types == nil {
		types = []string{}
	}
	return a.FilterTypes(types)
}

func (a *Artifacts) Types() []string {
	types := map[string]struct{}{}
	for _, artifact := range a.Entries {
		types[artifact.TypeName] = struct{}{}
	}
	return slices.Sorted(maps.Keys(types))
}

func (a *Artifacts) withPathFilter(matches func(*Artifact) bool, patterns []string) *Artifacts {
	selected := a.filter(matches)
	if a.Selector.Paths == nil {
		selected.Selector.Paths = patterns
	} else {
		selected.Selector.Paths = selected.exactPaths()
	}
	return selected
}

func (a *Artifacts) FilterPath(path []string) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool { return slices.Equal(path, artifact.Path) })
	// A literal path is not a pattern. Record only the matching paths so an
	// empty path, glob character, or different case cannot broaden URI().
	selected.Selector.Paths = selected.exactPaths()
	return selected
}

// FilterPattern keeps artifacts whose path matches the pattern, with the
// same glob rules as include patterns. A literal pattern matches one path
// exactly. The qualified path with the module name also matches, so an
// entrypoint artifact answers to both its shorthand and its full path.
func (a *Artifacts) FilterPattern(pattern string) (*Artifacts, error) {
	pattern = artifactPattern(pattern)
	var matchErr error
	selected := a.withPathFilter(func(artifact *Artifact) bool {
		match, err := artifact.matchesPattern(pattern)
		if err != nil {
			matchErr = err
		}
		return match
	}, []string{pattern})
	if matchErr != nil {
		return nil, matchErr
	}
	return selected, nil
}

// artifactPattern normalizes a path pattern to CLI case, as artifact paths are.
func artifactPattern(pattern string) string {
	segments := strings.Split(pattern, "/")
	for i, segment := range segments {
		segments[i] = strcase.ToKebab(segment)
	}
	return strings.Join(segments, "/")
}

// IncludePattern is the DAG address pattern with the same meaning as a
// Workspace.artifacts include pattern: a literal path also selects its
// children.
func IncludePattern(include string) string {
	pattern := artifactPattern(strings.ReplaceAll(include, ":", "/"))
	if strings.ContainsAny(pattern, "*?[{") {
		return pattern
	}
	return pattern + "/**"
}

func (a *Artifact) matchesPattern(pattern string) (bool, error) {
	if match, err := doublestar.PathMatch(pattern, strings.Join(a.Path, "/")); err != nil || match {
		return match, err
	}
	if a.Node == nil {
		return false, nil
	}
	return doublestar.PathMatch(pattern, strings.Join(a.Node.Path().CliCase(), "/"))
}

func (a *Artifacts) FilterDimensions(dimensions []string) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool {
		for _, key := range artifact.DimensionKeys {
			if slices.Contains(dimensions, key.Dimension) {
				return true
			}
		}
		return false
	})
	if len(dimensions) == 1 {
		selected.Selector.addDimension(ArtifactDimensionFilter{Dimension: dimensions[0]})
	} else {
		// Alternatives across dimensions have no query form; keep the paths.
		selected.Selector.Paths = selected.exactPaths()
	}
	return selected
}

func (a *Artifacts) FilterDimensionKeys(dimension string, keys []string) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool {
		for _, key := range artifact.DimensionKeys {
			if key.Dimension == dimension && slices.Contains(keys, key.Key) {
				return true
			}
		}
		return false
	})
	if keys == nil {
		keys = []string{}
	}
	selected.Selector.addDimension(ArtifactDimensionFilter{Dimension: dimension, Keys: slices.Clone(keys)})
	return selected
}

// addDimension combines a dimension filter with the selector: the same
// dimension intersects its keys, and a new dimension is another AND term.
func (sel *ArtifactSelector) addDimension(filter ArtifactDimensionFilter) {
	for i, existing := range sel.Dimensions {
		if existing.Dimension != filter.Dimension {
			continue
		}
		switch {
		case existing.Keys == nil:
			sel.Dimensions[i].Keys = filter.Keys
		case filter.Keys == nil:
		default:
			sel.Dimensions[i].Keys = slices.DeleteFunc(existing.Keys, func(key string) bool { return !slices.Contains(filter.Keys, key) })
		}
		return
	}
	sel.Dimensions = append(sel.Dimensions, filter)
}

func (a *Artifacts) Dimensions() []string {
	names := map[string]struct{}{}
	for _, artifact := range a.Entries {
		for _, key := range artifact.DimensionKeys {
			names[key.Dimension] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(names))
}

func (a *Artifacts) DimensionKeys(dimension string) []string {
	keys := map[string]struct{}{}
	for _, artifact := range a.Entries {
		for _, key := range artifact.DimensionKeys {
			if key.Dimension == dimension {
				keys[key.Key] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(keys))
}

// FilterURI applies a DAG address as one filter: the chain of path, type, and
// dimension-key filters the address encodes.
func (a *Artifacts) FilterURI(addr *dagaddress.Address) (*Artifacts, error) {
	selected := a
	if addr.Path != "" {
		var err error
		selected, err = selected.FilterPattern(addr.Path)
		if err != nil {
			return nil, err
		}
	}
	if len(addr.Types) > 0 {
		selected = selected.FilterTypeNames(addr.Types)
	}
	for _, filter := range addr.DimensionFilters() {
		if filter.Keys == nil {
			selected = selected.FilterDimensions([]string{filter.Dimension})
		} else {
			selected = selected.FilterDimensionKeys(filter.Dimension, filter.Keys)
		}
	}
	return selected, nil
}

// URI is the selector for the whole selection: filterUri(uri) selects the
// same set. Include patterns and chained filters are normalized; an empty
// selection prints as the empty alternation "{}".
func (a *Artifacts) URI() string {
	sel := a.Selector
	addr := &dagaddress.Address{HasScheme: true}
	empty := sel.Paths != nil && len(sel.Paths) == 0 || sel.Types != nil && len(sel.Types) == 0
	for _, dim := range sel.Dimensions {
		if dim.Keys != nil && len(dim.Keys) == 0 {
			empty = true
		}
	}
	if empty {
		addr.Path = "{}"
		return addr.String()
	}
	for _, typ := range slices.Sorted(slices.Values(sel.Types)) {
		if name := ArtifactTypeName(typ); !slices.Contains(addr.Types, name) {
			addr.Types = append(addr.Types, name)
		}
	}
	switch len(sel.Paths) {
	case 0:
	case 1:
		addr.Path = sel.Paths[0]
	default:
		addr.Path = "{" + strings.Join(sel.Paths, ",") + "}"
	}
	for _, dim := range sel.Dimensions {
		if dim.Keys == nil {
			addr.Query = append(addr.Query, dagaddress.Pair{Dimension: dim.Dimension})
			continue
		}
		for _, key := range dim.Keys {
			addr.Query = append(addr.Query, dagaddress.Pair{Dimension: dim.Dimension, Key: key, HasKey: true})
		}
	}
	return addr.String()
}

// One requires exactly one artifact. Several matches are listed, one address
// per line, so the caller can copy the correct one.
func (a *Artifacts) One() (*Artifact, error) {
	switch len(a.Entries) {
	case 1:
		return a.Entries[0].Clone(), nil
	case 0:
		return nil, fmt.Errorf("no artifact matches %s", a.URI())
	default:
		lines := make([]string, 0, len(a.Entries))
		for _, artifact := range a.Entries {
			uri, err := artifact.URI(ArtifactURIOpts{DimensionKeys: true})
			if err != nil {
				return nil, err
			}
			lines = append(lines, uri)
		}
		return nil, fmt.Errorf("%s matches %d artifacts:\n%s", a.URI(), len(a.Entries), strings.Join(lines, "\n"))
	}
}

// AssertType checks a type assertion from a DAG address against this artifact.
func (a *Artifact) AssertType(types []string) error {
	if len(types) == 0 || slices.Contains(types, ArtifactTypeName(a.TypeName)) {
		return nil
	}
	uri, err := a.URI(ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return err
	}
	return fmt.Errorf("%s is a %s, not %s", uri, a.TypeName, strings.Join(types, " or "))
}

// Evaluate selects the artifact's value in the caller's session. The cached
// module tree carries the dagql server that discovered it, whose field specs
// reference module provenance results owned by that session. A fresh server
// binds the provenance to results this session owns, so evaluation does not
// depend on the discovering session being alive.
func (a *Artifact) Evaluate(ctx context.Context, dest any) error {
	if a.Node == nil {
		return fmt.Errorf("artifact %s has no module tree", strings.Join(a.Path, "/"))
	}
	artifact := a.Clone()
	servers := map[uint64]*dagql.Server{}
	for node := artifact.Node; node != nil; node = node.Parent {
		if node.Module.Self() == nil {
			continue
		}
		moduleID, err := node.Module.ID()
		if err != nil {
			return fmt.Errorf("artifact module ID: %w", err)
		}
		key := moduleID.EngineResultID()
		srv, ok := servers[key]
		if !ok {
			srv, err = dagqlServerForModule(ctx, node.Module)
			if err != nil {
				return fmt.Errorf("artifact %s: %w", strings.Join(artifact.Path, "/"), err)
			}
			servers[key] = srv
		}
		node.DagqlServer = srv
	}
	return artifact.Node.DagqlValue(ctx, dest)
}

// ModuleArtifactNodes lists object values without evaluating them. Walk through
// module objects and stop at core objects. Skip caller arguments, nullable
// values, and lists.
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
		path := node.PathString()
		if !seen[path] {
			seen[path] = true
			nodes = append(nodes, node)
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
		return false, nil
	})
	return root, nodes, err
}

// Persist both individual artifacts and selections. One tree encoding shares
// module and type references across all entries in a selection.
type persistedArtifact struct {
	Path          []string
	DimensionKeys []*ArtifactDimensionKey
	TypeName      string
	Node          int
	Workspace     uint64
}
type persistedArtifacts struct {
	Tree     persistedModTree
	Entries  []persistedArtifact
	Selector ArtifactSelector
}

func encodeArtifacts(cache dagql.PersistedObjectCache, entries []*Artifact, selector ArtifactSelector) (dagql.PersistedObjectEncoding, error) {
	tree := newPersistedModTreeEncoder(cache)
	payload := persistedArtifacts{Selector: selector}
	for _, a := range entries {
		p := persistedArtifact{Path: a.Path, DimensionKeys: a.DimensionKeys, TypeName: a.TypeName}
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
	result := &Artifacts{Selector: payload.Selector}
	for _, p := range payload.Entries {
		a := &Artifact{Path: p.Path, DimensionKeys: p.DimensionKeys, TypeName: p.TypeName, Node: nodes[p.Node]}
		if a.DimensionKeys == nil {
			a.DimensionKeys = []*ArtifactDimensionKey{}
		}
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
	return encodeArtifacts(cache, []*Artifact{a}, ArtifactSelector{})
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
	return encodeArtifacts(cache, a.Entries, a.Selector)
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
