package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core/artifact"
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

// ArtifactPath describes a schema path without constructing its collections.
type ArtifactPath struct {
	ModuleName  string   `field:"true" doc:"The installed module name."`
	URI         string   `field:"true" doc:"The DAG address of this path, without dimension keys."`
	Description string   `field:"true" doc:"The description of the field at this path."`
	LoadError   string   `field:"true" doc:"A module load failure for this path, or an empty string."`
	Dimensions  []string `field:"true" doc:"The dimension identifiers required by this path."`
}

func (*ArtifactPath) Type() *ast.Type {
	return &ast.Type{NamedType: "ArtifactPath", NonNull: true}
}

func (*ArtifactPath) TypeDescription() string {
	return "A schema path and its dimensions. The path can exist even when its collections have no runtime items."
}

// Artifact holds a complete address and its deferred object value. The module
// tree and workspace are retained so evaluation does not depend on the caller.
//
// A workspace artifact has a Workspace and a tree rooted at a fresh
// construction of its module. A bound artifact (see BoundArtifacts) has no
// Workspace: its tree is rooted at a live object value (ModTreeNode.RootValue),
// e.g. an LLM's bound tool object. It evaluates in its ContextWorkspace, if
// any, else in the caller's context.
type Artifact struct {
	ModuleName     string `field:"true" doc:"The installed module name."`
	DimensionNames map[string]string
	Path           []string                `field:"true" doc:"Ordered, literal fields to follow. Workspace main targets use their shorthand."`
	DimensionKeys  []*ArtifactDimensionKey `field:"true" doc:"The module name, collection keys, and full path key in the artifact type dimension."`
	TypeName       string
	// TypeKeyName is the shortest type key that names this path among the
	// paths with its collection dimensions (see artifact.ShortTypeKey), or
	// empty for the full path. Addresses with item keys print it.
	TypeKeyName string
	Directives  []string `field:"true" doc:"The directives carried by this artifact."`
	LoadFailure *ModuleLoadFailure
	Node        *ModTreeNode
	// Workspace is the workspace that supplied the artifact: its address is
	// relative to it, and it names the artifact's identity. Unset for a
	// bound artifact.
	Workspace dagql.ObjectResult[*Workspace]
	// ContextWorkspace is the workspace a bound artifact evaluates in — for an
	// LLM's scope, the conversation's workspace — so a required Workspace
	// argument along its path is the same whoever evaluates it, a module
	// function included. It is not part of the address or identity; a
	// workspace artifact evaluates in its own Workspace instead.
	ContextWorkspace dagql.ObjectResult[*Workspace]
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
	// Full prints the stored form of an address with keys: the full path in
	// the type key, and every item key of a batch. Without it, an address
	// with keys is a projection, the shortest that selects the same artifact
	// in the loaded workspace: a short type key (see TypeKeyName), and a
	// batch that runs its whole collection named by its parent keys. A schema
	// change can alter a projection, so select by the full address whatever
	// must keep its meaning.
	Full bool
}

// URI is the artifact's DAG address. See hack/designs/collections-issue.md,
// section 4.
func (a *Artifact) URI(opts ArtifactURIOpts) (string, error) {
	var keys []dagaddress.Pair
	var dimensions []string
	if opts.DimensionKeys {
		var collapsed string
		if !opts.Full {
			collapsed = a.completeBatchDimension()
		}
		for _, key := range a.DimensionKeys {
			if artifact.IsStaticDimension(key.Dimension) || key.Dimension == collapsed {
				continue
			}
			keys = append(keys, dagaddress.Pair{Dimension: a.dimensionName(key.Dimension), Key: key.Key, HasKey: true})
			if !slices.Contains(dimensions, key.Dimension) {
				dimensions = append(dimensions, key.Dimension)
			}
		}
	}
	// The type dimension key is the path, so the address needs no path.
	addr := &dagaddress.Address{HasScheme: true}
	path := strings.Join(a.Path, "/")
	if a.TypeName == "" {
		addr.Path = path
	} else {
		// The short key names this path only alongside the keys it was
		// chosen for.
		key := path
		if !opts.Full && a.TypeKeyName != "" && len(dimensions) > 0 && slices.Equal(dimensions, a.projectedDimensions()) {
			key = a.TypeKeyName
		}
		addr.Query = append(addr.Query, dagaddress.Pair{Dimension: a.dimensionName("type:" + a.TypeName), Key: key, HasKey: true})
	}
	addr.Query = append(addr.Query, keys...)
	if opts.TypeAssertion {
		addr.Types = []string{ArtifactTypeName(a.TypeName)}
	}
	if opts.Absolute {
		if a.Workspace.Self() == nil {
			return "", fmt.Errorf("%s has no absolute address: it is not a workspace artifact", addr.String())
		}
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

// StaleArtifact is the staleness check of a generator artifact: a Check at the
// generator's path plus stale, with the generator's keys. It prints the full
// path in its type key, which names it whatever the loaded scope.
func (a *Artifact) StaleArtifact() *Artifact {
	stale := a.Clone()
	stale.Path = append(slices.Clone(a.Path), "stale")
	stale.TypeName = "Check"
	stale.TypeKeyName = ""
	stale.setStaticDimensions()
	return stale
}

// projectedDimensions are the collection dimensions a projected address keys:
// every collection dimension on the path, except the dimension of a batch
// that runs its whole collection.
func (a *Artifact) projectedDimensions() []string {
	collapsed := a.completeBatchDimension()
	var dimensions []string
	for _, dim := range a.DimensionDefinitions() {
		if !artifact.IsStaticDimension(dim.Identifier) && dim.Identifier != collapsed && !slices.Contains(dimensions, dim.Identifier) {
			dimensions = append(dimensions, dim.Identifier)
		}
	}
	return dimensions
}

// completeBatchDimension is the dimension of a batch receiver that runs every
// item of its collection (see Artifacts.Batch), or "".
func (a *Artifact) completeBatchDimension() string {
	for node := a.Node; node != nil; node = node.Parent {
		if node.CollectionDimension != nil && node.CollectionKeys != nil {
			if node.CollectionComplete {
				return node.CollectionDimension.Identifier
			}
			return ""
		}
	}
	return ""
}

// Description is the description of the field that supplies the artifact. A
// load failure describes the module that could not be loaded; a generator's
// stale check is described from its generator.
func (a *Artifact) Description() string {
	if a.LoadFailure != nil {
		if a.Workspace.Self() == nil {
			return "this bound tool object could not be loaded"
		}
		return "this workspace module could not be loaded"
	}
	if a.Node == nil {
		return ""
	}
	generator := a.Node.Parent
	if a.Node.Name == "stale" && generator != nil && slices.Contains(generator.Directives, "generate") {
		if obj := generator.ObjectType(); obj != nil && obj.Name == "Generator" && obj.SourceModuleName == "" {
			description, _, _ := strings.Cut(generator.Description, "\n")
			description = strings.TrimRight(strings.TrimSpace(description), ".:;!?")
			if description == "" {
				return "staleness check"
			}
			first, size := utf8.DecodeRuneInString(description)
			return "staleness check: " + string(unicode.ToLower(first)) + description[size:]
		}
	}
	return a.Node.Description
}

// PathDefinitions lists the entries' schema paths, one per address (without
// dimension keys), sorted by address. It reads no runtime values: apply
// SchemaSelection first so the selection's filters are bound.
func (a *Artifacts) PathDefinitions(opts ArtifactURIOpts) ([]*ArtifactPath, error) {
	opts.DimensionKeys = false
	paths := map[string]*ArtifactPath{}
	for _, entry := range a.Entries {
		uri, err := entry.URI(opts)
		if err != nil {
			return nil, err
		}
		path := paths[uri]
		if path == nil {
			path = &ArtifactPath{ModuleName: entry.ModuleName, URI: uri, Description: entry.Description(), Dimensions: []string{}}
			paths[uri] = path
		}
		if entry.LoadFailure != nil {
			path.LoadError = entry.LoadFailure.Message
		}
		for _, dimension := range entry.DimensionDefinitions() {
			if !slices.Contains(path.Dimensions, dimension.Identifier) {
				path.Dimensions = append(path.Dimensions, dimension.Identifier)
			}
		}
	}
	result := make([]*ArtifactPath, 0, len(paths))
	for _, path := range paths {
		result = append(result, path)
	}
	slices.SortFunc(result, func(a, b *ArtifactPath) int { return strings.Compare(a.URI, b.URI) })
	return result, nil
}

// artifactReferenceChainKey carries the chain of artifact references being
// resolved in a context, to detect reference cycles.
type artifactReferenceChainKey struct{}

// WithArtifactReference records that the reference ref (a canonical address)
// is being resolved in ctx. It fails if ref is already being resolved further
// up the chain: evaluating it would construct itself, e.g. through a module
// whose constructor defaults to an address of its own artifact.
func WithArtifactReference(ctx context.Context, ref string) (context.Context, error) {
	chain, _ := ctx.Value(artifactReferenceChainKey{}).([]string)
	if slices.Contains(chain, ref) {
		return nil, fmt.Errorf("module reference cycle detected: %s -> %s", strings.Join(chain, " -> "), ref)
	}
	return context.WithValue(ctx, artifactReferenceChainKey{}, append(slices.Clip(chain), ref)), nil
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
	// Each group requires any one dimension; separate groups use AND.
	DimensionAlternatives [][]string
	// Explicit collection endpoints select the collection unless its own
	// dimension is requested. Keep this distinct from normalized path patterns.
	CollectionPaths []string
	// Apply keyed exclusions after collection expansion.
	ExcludedURIs []string
}

// Artifacts is an immutable selection. Filtering changes only the entry list
// and the recorded selector, preserving each artifact's address, module tree,
// and workspace.
type Artifacts struct {
	Entries  []*Artifact
	Selector ArtifactSelector
	// AllDimensions lists the dimensions of every loaded module. Names resolve
	// against it, so a filter cannot change what a name means. It omits
	// modules that are not loaded, but names start with their module, so
	// those modules cannot change a name either.
	AllDimensions artifact.Dimensions
	// AllPaths lists the schema paths of every loaded module. Short type keys
	// resolve and print against it, as names do against AllDimensions.
	AllPaths artifact.Paths
	// exact makes load failures match filters only by their own path and
	// keys, not by what their module could have held (see
	// Artifact.mayContainPath). Exclusions and resolution use it internally.
	exact bool
}

var _ dagql.PersistedObject = (*Artifact)(nil)
var _ dagql.PersistedObjectDecoder = (*Artifact)(nil)
var _ dagql.HasDependencyResults = (*Artifact)(nil)
var _ dagql.PersistedObject = (*Artifacts)(nil)
var _ dagql.PersistedObjectDecoder = (*Artifacts)(nil)
var _ dagql.HasDependencyResults = (*Artifacts)(nil)

func (*Artifacts) Type() *ast.Type { return &ast.Type{NamedType: "Artifacts", NonNull: true} }
func (*Artifacts) TypeDescription() string {
	return "An immutable selection of workspace artifacts. Listed types, dimensions, and keys use OR; chained filters use AND. Empty alternatives match nothing; unknown names match nothing but module load failures. Filters never change addresses or dimension identifiers. Module keys match a module's configured or kebab-case name. A module that failed to load has no schema, so its load failure stands in for whatever it would hold: a filter keeps it when the module could hold something the filter selects, and an exclusion drops it only by its own address, such as <module>/** or ?module=<module>."
}

func (a *Artifacts) filter(matches func(*Artifact) bool) *Artifacts {
	selected := &Artifacts{Entries: make([]*Artifact, 0, len(a.Entries)), Selector: a.Selector.clone(), AllDimensions: a.AllDimensions, AllPaths: a.AllPaths, exact: a.exact}
	for _, artifact := range a.Entries {
		if matches(artifact) {
			selected.Entries = append(selected.Entries, artifact.Clone())
		}
	}
	return selected
}

func (sel ArtifactSelector) clone() ArtifactSelector {
	cloned := ArtifactSelector{
		Paths:           slices.Clone(sel.Paths),
		Types:           slices.Clone(sel.Types),
		CollectionPaths: slices.Clone(sel.CollectionPaths),
		ExcludedURIs:    slices.Clone(sel.ExcludedURIs),
	}
	for _, dim := range sel.Dimensions {
		cloned.Dimensions = append(cloned.Dimensions, ArtifactDimensionFilter{Dimension: dim.Dimension, Keys: slices.Clone(dim.Keys)})
	}
	for _, group := range sel.DimensionAlternatives {
		cloned.DimensionAlternatives = append(cloned.DimensionAlternatives, slices.Clone(group))
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

func (a *Artifacts) FilterDirectives(directives []string, exclude bool) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool {
		return slices.ContainsFunc(artifact.Directives, func(dir string) bool { return slices.Contains(directives, dir) }) != exclude
	})
	selected.Selector.Paths = selected.exactPaths()
	return selected
}

// FilterTypes keeps artifacts of any listed type. A module that failed to
// load has no schema, so its load failure can hide artifacts of any type. The
// failure stays in the selection, unless the filter can match nothing, so
// consumers report the error instead of an incomplete result.
func (a *Artifacts) FilterTypes(types []string, exclude bool) *Artifacts {
	return a.filterTypes(types, exclude, exclude || len(types) > 0)
}

func (a *Artifacts) filterTypes(types []string, exclude, keepLoadFailures bool) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool {
		if artifact.LoadFailure != nil && keepLoadFailures {
			return true
		}
		return slices.Contains(types, artifact.TypeName) != exclude
	})
	if exclude {
		selected.Selector.Paths = selected.exactPaths()
		return selected
	}
	if a.Selector.Types == nil {
		selected.Selector.Types = slices.Clone(types)
	} else {
		selected.Selector.Types = slices.DeleteFunc(selected.Selector.Types, func(typ string) bool { return !slices.Contains(types, typ) })
	}
	return selected
}

// Parent filters inspect the immediately preceding object in the schema path.
// A root or module-load failure has no typed parent and does not match.
func (a *Artifacts) FilterParentTypes(types []string, exclude bool) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool {
		match := false
		if artifact.Node != nil {
			if parent := artifact.Node.Parent.ObjectType(); parent != nil {
				match = slices.Contains(types, parent.Name)
			}
		}
		return match != exclude
	})
	selected.Selector.Paths = selected.exactPaths()
	return selected
}

func (a *Artifacts) FilterParentDirectives(directives []string, exclude bool) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool {
		match := false
		if artifact.Node != nil && artifact.Node.Parent != nil {
			match = slices.ContainsFunc(artifact.Node.Parent.Directives, func(dir string) bool { return slices.Contains(directives, dir) })
		}
		return match != exclude
	})
	selected.Selector.Paths = selected.exactPaths()
	return selected
}

// identity distinguishes addresses in different workspaces, or rooted at
// different bound values, even if their values happen to be the same object.
func (a *Artifact) identity() (string, error) {
	scope, err := a.scope()
	if err != nil {
		return "", err
	}
	return scope + ":" + artifactIdentity(a), nil
}

// scope names what the artifact's address is relative to: its workspace
// ("w<result ID>"), else the live value its tree is rooted at ("v<result ID>"),
// else nothing ("").
func (a *Artifact) scope() (string, error) {
	if a.Workspace.Self() != nil {
		id, err := a.Workspace.ID()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("w%d", id.EngineResultID()), nil
	}
	if root := a.BoundRoot(); root != nil {
		id, err := root.ID()
		if err != nil {
			return "", fmt.Errorf("artifact %s root: %w", strings.Join(a.Path, "/"), err)
		}
		return fmt.Sprintf("v%d", id.EngineResultID()), nil
	}
	return "", nil
}

// BoundRoot returns the live value a bound artifact's tree is rooted at, or
// nil for a workspace artifact, which is evaluated from a fresh construction.
func (a *Artifact) BoundRoot() dagql.AnyObjectResult {
	if root := a.boundRootNode(); root != nil {
		return root.RootValue
	}
	return nil
}

// boundRootNode returns the node a bound artifact's tree is rooted at: the
// one holding the live value. Nil for a workspace artifact.
func (a *Artifact) boundRootNode() *ModTreeNode {
	if a.Workspace.Self() != nil {
		return nil
	}
	for node := a.Node; node != nil; node = node.Parent {
		if node.RootValue != nil {
			return node
		}
	}
	return nil
}

// unqualifiedPath is the path of a bound artifact whose tree was qualified by
// its bound type (see qualifyCollidingTrees) without that segment: the path
// the address would have had if no other bound object collided with it. Nil
// for any other artifact.
func (a *Artifact) unqualifiedPath() []string {
	root := a.boundRootNode()
	if root == nil || root.Parent == nil || root.Parent.Name == "" {
		return nil
	}
	path := a.Node.Path().CliCase()
	qualifier := len(root.Path()) - 1
	if qualifier < 0 || qualifier >= len(path) {
		return nil
	}
	return slices.Delete(path, qualifier, qualifier+1)
}

// WorkspaceContext binds the workspace the artifact evaluates in into ctx:
// its owning client for host routing, and the workspace itself for
// contextual and Workspace-typed arguments. That is the artifact's own
// Workspace, else its ContextWorkspace (see BoundArtifacts); with neither,
// ctx is left as is.
func (a *Artifact) WorkspaceContext(ctx context.Context) (context.Context, error) {
	ws := a.Workspace
	if ws.Self() == nil {
		ws = a.ContextWorkspace
	}
	if ws.Self() == nil {
		return ctx, nil
	}
	ctx, err := WorkspaceClientContext(ctx, ws.Self())
	if err != nil {
		return nil, err
	}
	return WorkspaceToContext(ctx, ws), nil
}

func (a *Artifacts) WithArtifacts(other *Artifacts) (*Artifacts, error) {
	selected := &Artifacts{Entries: []*Artifact{}, AllDimensions: artifact.Union(a.AllDimensions, other.AllDimensions), AllPaths: artifact.UnionPaths(a.AllPaths, other.AllPaths)}
	seen := map[string]bool{}
	for _, artifact := range slices.Concat(a.Entries, other.Entries) {
		key, err := artifact.identity()
		if err != nil {
			return nil, err
		}
		if !seen[key] {
			seen[key] = true
			selected.Entries = append(selected.Entries, artifact.Clone())
		}
	}
	slices.SortStableFunc(selected.Entries, func(a, b *Artifact) int { return slices.Compare(a.Path, b.Path) })
	selected.Selector.Paths = selected.exactPaths()
	return selected, nil
}

func (a *Artifacts) WithoutArtifacts(other *Artifacts) (*Artifacts, error) {
	excluded := map[string]bool{}
	for _, artifact := range other.Entries {
		key, err := artifact.identity()
		if err != nil {
			return nil, err
		}
		excluded[key] = true
	}
	var matchErr error
	selected := a.filter(func(artifact *Artifact) bool {
		key, err := artifact.identity()
		if err != nil {
			matchErr = err
		}
		return !excluded[key]
	})
	selected.Selector.Paths = selected.exactPaths()
	return selected, matchErr
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
	return a.filterTypes(types, false, len(names) > 0)
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
	// A question mark is a path glob here, but starts the query in a DAG link.
	if a.Selector.Paths == nil && !slices.ContainsFunc(patterns, func(pattern string) bool { return strings.Contains(pattern, "?") }) {
		selected.Selector.Paths = patterns
	} else {
		selected.Selector.Paths = selected.exactPaths()
	}
	return selected
}

func (a *Artifacts) FilterPath(path []string) *Artifacts {
	selected := a.filter(func(artifact *Artifact) bool {
		return slices.Equal(path, artifact.Path) || !a.exact && artifact.mayContainPath(strings.Join(path, "/"), false)
	})
	// A literal path is not a pattern. Record only the matching paths so an
	// empty path, glob character, or different case cannot broaden URI().
	selected.Selector.Paths = selected.exactPaths()
	selected.Selector.CollectionPaths = append(selected.Selector.CollectionPaths, strings.Join(path, "/"))
	return selected
}

// FilterPattern keeps artifacts whose path matches the pattern, with the
// same glob rules as include patterns. A literal pattern matches one path
// exactly. The qualified path with the module name also matches, so an
// workspace main artifact answers to both its shorthand and its full path.
func (a *Artifacts) FilterPattern(pattern string) (*Artifacts, error) {
	pattern = artifactPattern(pattern)
	var matchErr error
	selected := a.withPathFilter(func(artifact *Artifact) bool {
		match, err := artifact.matchesPattern(pattern)
		if err != nil {
			matchErr = err
		}
		return match || !a.exact && artifact.mayContainPath(pattern, true)
	}, []string{pattern})
	if matchErr != nil {
		return nil, matchErr
	}
	if !strings.ContainsAny(pattern, "*?[{") {
		// Only artifacts are collection endpoints, not load failures.
		endpoints := selected.filter(func(artifact *Artifact) bool { return artifact.LoadFailure == nil })
		selected.Selector.CollectionPaths = append(selected.Selector.CollectionPaths, endpoints.exactPaths()...)
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
	return pattern + "/**"
}

func (a *Artifact) matchesPattern(pattern string) (bool, error) {
	if match, err := doublestar.PathMatch(pattern, strings.Join(a.Path, "/")); err != nil || match {
		return match, err
	}
	if a.Node == nil {
		return false, nil
	}
	if match, err := doublestar.PathMatch(pattern, strings.Join(a.Node.Path().CliCase(), "/")); err != nil || match {
		return match, err
	}
	// A bound tree qualified by its type still answers to the plain path,
	// which then selects the artifact of every colliding bound object.
	if path := a.unqualifiedPath(); path != nil {
		return doublestar.PathMatch(pattern, strings.Join(path, "/"))
	}
	return false, nil
}

func (a *Artifacts) FilterDimensions(dimensions []string) *Artifacts {
	selected := a.filter(func(*Artifact) bool { return true })
	if len(dimensions) == 1 {
		selected.Selector.addDimension(ArtifactDimensionFilter{Dimension: dimensions[0]})
	} else {
		selected.Selector.DimensionAlternatives = append(selected.Selector.DimensionAlternatives, slices.Clone(dimensions))
	}
	return selected
}

func (a *Artifacts) FilterDimensionKeys(dimension string, keys []string) *Artifacts {
	selected := a.filter(func(*Artifact) bool { return true })
	if keys == nil {
		keys = []string{}
	}
	selected.Selector.addDimension(ArtifactDimensionFilter{Dimension: dimension, Keys: slices.Clone(keys)})
	return selected
}

// addDimension combines a dimension filter with the selector: the same
// dimension intersects its keys, and a new dimension is another AND term.
func (sel *ArtifactSelector) addDimension(filter ArtifactDimensionFilter) {
	filter.Keys = slices.Clone(filter.Keys)
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
// dimension-key filters the address encodes. An absolute address names a
// workspace, so it never selects an artifact without one (see BoundArtifacts);
// callers check that the workspace is the selection's own.
//
// Like the filters it chains, it keeps a module's load failure when the
// module could have held what the address selects (see
// Artifact.mayContainPath and Artifact.mayHaveDimension).
func (a *Artifacts) FilterURI(addr *dagaddress.Address) (*Artifacts, error) {
	return a.filterURI(addr, false)
}

// filterURI is FilterURI; with exact, a load failure matches only by its own
// path and keys. Exclusions are exact: excluding part of a broken module must
// not hide its failure, while excluding all of it (dag://staff/**) still
// does. Resolution is exact too: a load failure is not the artifact it names.
func (a *Artifacts) filterURI(addr *dagaddress.Address, exact bool) (*Artifacts, error) {
	if exact != a.exact {
		a = a.filter(func(*Artifact) bool { return true })
		a.exact = exact
	}
	selected := a
	if addr.Absolute && slices.ContainsFunc(a.Entries, func(artifact *Artifact) bool { return artifact.Workspace.Self() == nil }) {
		selected = a.filter(func(artifact *Artifact) bool { return artifact.Workspace.Self() != nil })
		selected.Selector.Paths = selected.exactPaths()
	}
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
		// A normalized wildcard can leave one collection endpoint. Preserve
		// its pattern form so a URI round trip still includes the items.
		if !strings.ContainsAny(addr.Path, "*?[{") && !slices.Contains(sel.CollectionPaths, addr.Path) && a.hasCollections() {
			addr.Path = "{" + addr.Path + "}"
		}
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

// ErrNoArtifactMatch reports a selection that requires one artifact and has
// none.
var ErrNoArtifactMatch = errors.New("no artifact matches")

// One requires exactly one artifact. Several matches are listed, one address
// per line, so the caller can copy the correct one.
func (a *Artifacts) One() (*Artifact, error) {
	switch len(a.Entries) {
	case 1:
		return a.Entries[0].Clone(), nil
	case 0:
		return nil, fmt.Errorf("%w %s", ErrNoArtifactMatch, a.URI())
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
	if a.LoadFailure != nil {
		// A load failure is a failed check. The module has no schema to
		// show that it has no artifact of the asserted type.
		return errors.New(a.LoadFailure.Message)
	}
	uri, err := a.URI(ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return err
	}
	return fmt.Errorf("%s is a %s, not %s", uri, a.TypeName, strings.Join(types, " or "))
}

// ResolveURI selects the one artifact a DAG address names. The address's path
// and dimension keys filter the selection; its type assertion only chooses
// among several matches, so a single artifact of another type is left for
// AssertType to report. expand enumerates the selected collections' keys.
func (a *Artifacts) ResolveURI(ctx context.Context, addr *dagaddress.Address, expand func(context.Context, *Artifacts) (*Artifacts, error)) (*Artifact, error) {
	untyped := *addr
	untyped.Types = nil
	// Resolution names one artifact; a load failure is not it.
	selected, err := a.filterURI(&untyped, true)
	if err != nil {
		return nil, err
	}
	if len(selected.Entries) > 1 && len(addr.Types) > 0 {
		selected = selected.FilterTypeNames(addr.Types)
	}
	selected, err = expand(ctx, selected)
	if err != nil {
		return nil, err
	}
	if len(selected.Entries) == 0 {
		return nil, a.noArtifactMatches(&untyped)
	}
	return selected.One()
}

// noArtifactMatches reports an address that resolves to nothing, naming the
// modules that failed to load and could have held it.
func (a *Artifacts) noArtifactMatches(addr *dagaddress.Address) error {
	err := ErrNoArtifactMatch
	failures, filterErr := a.filterURI(addr, false)
	if filterErr == nil {
		failures, filterErr = failures.SchemaSelection()
	}
	if filterErr != nil {
		return err
	}
	for _, failure := range failures.Entries {
		if failure.LoadFailure != nil {
			err = fmt.Errorf("%w; module %q failed to load: %s", err, failure.LoadFailure.Name, failure.LoadFailure.Message)
		}
	}
	return err
}

// BoundArtifacts discovers the artifacts of a module rooted at a live value of
// one of its objects rather than a fresh construction of its main object:
// evaluating one selects its path from root. Paths are qualified with the
// module name, as a workspace module's are, but never use workspace main
// shorthand. Unlike workspace discovery, a constructor that requires
// arguments does not hide the tree, since it is never called.
func BoundArtifacts(ctx context.Context, mod dagql.ObjectResult[*Module], root dagql.AnyObjectResult) (*Artifacts, error) {
	tree, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}
	// Root the tree at the bound value's own type: the main object, or
	// another object of the module (e.g. a narrower view of it).
	rootType, ok := tree.types[root.Type().Name()]
	if !ok {
		return nil, fmt.Errorf("module %q has no object type %q", mod.Self().Name(), root.Type().Name())
	}
	tree.Type = rootType
	var nodes []*ModTreeNode
	if err := walkArtifactNodes(ctx, tree, func(node *ModTreeNode) { nodes = append(nodes, node) }, map[string]bool{}); err != nil {
		return nil, err
	}
	// Name the root after the module under a synthetic parent, like a
	// workspace module root, so paths and dimension names match.
	tree.RootValue = root
	tree.Parent = &ModTreeNode{}
	tree.Name = mod.Self().Name()
	artifacts := &Artifacts{Entries: []*Artifact{}}
	for _, node := range nodes {
		if node == tree {
			continue
		}
		artifacts.Entries = append(artifacts.Entries, &Artifact{
			ModuleName: tree.Name, Path: node.Path().CliCase(), DimensionKeys: []*ArtifactDimensionKey{},
			Directives: node.Directives, TypeName: node.ObjectType().Name, Node: node,
		})
	}
	slices.SortFunc(artifacts.Entries, func(a, b *Artifact) int { return slices.Compare(a.Path, b.Path) })
	artifacts.AllDimensions = artifacts.DimensionDefinitions()
	artifacts.AllPaths = artifacts.PathIndex()
	return artifacts, nil
}

// Evaluate selects the artifact's value in the caller's session. The cached
// module tree carries the dagql server that discovered it, whose field specs
// reference module provenance results owned by that session. A fresh server
// binds the provenance to results this session owns, so evaluation does not
// depend on the discovering session being alive.
func (a *Artifact) Evaluate(ctx context.Context, dest any, inputs ...dagql.NamedInput) error {
	if a.Node == nil {
		return fmt.Errorf("artifact %s has no module tree", strings.Join(a.Path, "/"))
	}
	artifact := a.Clone()
	servers := map[uint64]*dagql.Server{}
	for node := artifact.Node; node != nil; node = node.Parent {
		if node.Module.Self() == nil {
			srv, err := CurrentDagqlServer(ctx)
			if err != nil {
				return err
			}
			node.DagqlServer = srv
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
	return artifact.Node.dagqlValue(ctx, dest, inputs)
}

// ModuleArtifactNodes discovers object fields without evaluating their values.
// Module fields are artifacts; core fields are artifacts only with a target
// directive. Both can lead to further artifacts.
func ModuleArtifactNodes(ctx context.Context, mod dagql.ObjectResult[*Module]) (*ModTreeNode, []*ModTreeNode, error) {
	root, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, nil, err
	}
	nodes, err := ArtifactNodes(ctx, root)
	return root, nodes, err
}

func ArtifactNodes(ctx context.Context, root *ModTreeNode) ([]*ModTreeNode, error) {
	if obj := root.ObjectType(); obj != nil && obj.Constructor.Valid && functionRequiresCallerArgs(obj.Constructor.Value.Self()) {
		return nil, nil
	}
	var nodes []*ModTreeNode
	err := walkArtifactNodes(ctx, root, func(node *ModTreeNode) { nodes = append(nodes, node) }, map[string]bool{})
	return nodes, err
}

// Persist both individual artifacts and selections. One tree encoding shares
// module and type references across all entries in a selection.
type persistedArtifact struct {
	ModuleName     string
	DimensionNames map[string]string
	LoadFailure    *ModuleLoadFailure
	Path           []string
	DimensionKeys  []*ArtifactDimensionKey
	TypeName       string
	TypeKeyName    string `json:",omitempty"`
	Directives     []string
	Node           int
	Workspace      uint64
	// ContextWorkspace is Artifact.ContextWorkspace; zero when unset.
	ContextWorkspace uint64 `json:",omitempty"`
}
type persistedArtifacts struct {
	Tree          persistedModTree
	Entries       []persistedArtifact
	Selector      ArtifactSelector
	AllDimensions artifact.Dimensions
	AllPaths      artifact.Paths `json:",omitempty"`
}

func encodeArtifacts(enc *dagql.PersistEncodeContext, entries []*Artifact, selector ArtifactSelector, allDimensions artifact.Dimensions, allPaths artifact.Paths) (dagql.PersistedObjectEncoding, error) {
	tree := newPersistedModTreeEncoder(enc)
	payload := persistedArtifacts{Selector: selector, AllDimensions: allDimensions, AllPaths: allPaths}
	for _, a := range entries {
		p := persistedArtifact{ModuleName: a.ModuleName, LoadFailure: a.LoadFailure, Path: a.Path, DimensionKeys: a.DimensionKeys, TypeName: a.TypeName, TypeKeyName: a.TypeKeyName, Directives: a.Directives}
		p.DimensionNames = a.DimensionNames
		var err error
		p.Node, err = tree.Add(a.Node)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		if a.Workspace.Self() != nil {
			p.Workspace, err = encodePersistedObjectRef(enc, a.Workspace, "artifact workspace")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
		}
		if a.ContextWorkspace.Self() != nil {
			p.ContextWorkspace, err = encodePersistedObjectRef(enc, a.ContextWorkspace, "artifact context workspace")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
		}
		payload.Entries = append(payload.Entries, p)
	}
	payload.Tree = tree.tree
	return encodePersistedObjectPayload(payload)
}
func decodeArtifacts(ctx context.Context, dec *dagql.PersistDecodeContext, raw json.RawMessage) (*Artifacts, error) {
	var payload persistedArtifacts
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	nodes, err := decodePersistedModTree(ctx, dec, payload.Tree)
	if err != nil {
		return nil, err
	}
	result := &Artifacts{Selector: payload.Selector, AllDimensions: payload.AllDimensions, AllPaths: payload.AllPaths}
	for _, p := range payload.Entries {
		a := &Artifact{ModuleName: p.ModuleName, LoadFailure: p.LoadFailure, Path: p.Path, DimensionKeys: p.DimensionKeys, TypeName: p.TypeName, TypeKeyName: p.TypeKeyName, Directives: p.Directives, Node: nodes[p.Node]}
		a.DimensionNames = p.DimensionNames
		if a.DimensionKeys == nil {
			a.DimensionKeys = []*ArtifactDimensionKey{}
		}
		if a.Node == nil && a.LoadFailure == nil {
			return nil, fmt.Errorf("artifact references missing tree node %d", p.Node)
		}
		a.Workspace, err = loadPersistedObjectResultByResultID[*Workspace](ctx, dec, p.Workspace, "artifact workspace")
		if err != nil {
			return nil, err
		}
		a.ContextWorkspace, err = loadPersistedObjectResultByResultID[*Workspace](ctx, dec, p.ContextWorkspace, "artifact context workspace")
		if err != nil {
			return nil, err
		}
		result.Entries = append(result.Entries, a)
	}
	return result, nil
}
func (a *Artifact) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return encodeArtifacts(enc, []*Artifact{a}, ArtifactSelector{}, nil, nil)
}
func (*Artifact) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, raw json.RawMessage) (dagql.Typed, error) {
	result, err := decodeArtifacts(ctx, dec, raw)
	if err != nil {
		return nil, err
	}
	if len(result.Entries) != 1 {
		return nil, fmt.Errorf("invalid persisted artifact count: %d", len(result.Entries))
	}
	return result.Entries[0], nil
}
func (a *Artifacts) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return encodeArtifacts(enc, a.Entries, a.Selector, a.AllDimensions, a.AllPaths)
}
func (*Artifacts) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, raw json.RawMessage) (dagql.Typed, error) {
	return decodeArtifacts(ctx, dec, raw)
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
	if a.ContextWorkspace.Self() != nil {
		value, err := attach(a.ContextWorkspace)
		if err != nil {
			return nil, err
		}
		var ok bool
		a.ContextWorkspace, ok = value.(dagql.ObjectResult[*Workspace])
		if !ok {
			return nil, fmt.Errorf("artifact context workspace has unexpected type %T", value)
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

func (a *Artifacts) WithoutURI(address *dagaddress.Address) (*Artifacts, error) {
	if a.hasCollections() {
		for _, filter := range address.DimensionFilters() {
			if filter.Keys == nil {
				continue
			}
			dimension, err := a.ResolveDimension(filter.Dimension)
			if err != nil {
				return nil, err
			}
			if !strings.HasPrefix(dimension, "type:") {
				selected := a.filter(func(*Artifact) bool { return true })
				selected.Selector.ExcludedURIs = append(selected.Selector.ExcludedURIs, address.String())
				return selected, nil
			}
		}
	}
	excluded, err := a.filterURI(address, true)
	if err != nil {
		return nil, err
	}
	excluded, err = excluded.SchemaSelection()
	if err != nil {
		return nil, err
	}
	return a.WithoutArtifacts(excluded)
}
