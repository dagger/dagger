package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
)

// findArtifactsToolName is the builtin that lists the conversation's scope
// (MCP.Artifacts): the agent's `dagger list`.
const findArtifactsToolName = "FindArtifacts"

// FindArtifacts views.
const (
	findArtifactsViewPaths = "paths"
	findArtifactsViewKeys  = "keys"
	findArtifactsViewItems = "items"
)

// loadArtifactTools registers FindArtifacts when the conversation's scope can
// be non-empty. The scope itself is computed per call, never here: listing
// tools must stay cheap.
func (m *MCP) loadArtifactTools(srv *dagql.Server, allTools *LLMToolSet) {
	if !m.mayHaveArtifacts(srv) {
		return
	}
	allTools.Add(LLMTool{
		Name: findArtifactsToolName,
		Description: "List the artifacts you can name by DAG address -- the values tool arguments accept as addresses (e.g. a GitRef or Directory argument) -- like `dagger list`. " +
			"The scope is the workspace's modules plus the modules of your bound tool objects, which are read from their current state and shadow a workspace module of the same name (unless they are a fresh construction of it). Listing never evaluates a value." + "\n" +
			"Address grammar: dag[+<type>]://<module>/<field>/...[?<dimension>=<key>&...], e.g. dag+git-ref://staff/members/head?member=chief. " +
			"The path follows fields from the module; each collection on the path needs a key for its dimension; +<type> asserts the artifact's type (CLI case)." + "\n" +
			"With no arguments: an overview of types, collections and load errors. Filters combine with AND. " +
			"Views: paths lists each schema path once, with a <key> placeholder per dimension it still needs (the default); items lists every fully keyed address, ready to pass to a tool (the default once a collection key is selected); keys lists one dimension's keys.",
		ReadOnly: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"address": map[string]any{
					"type":        "string",
					"description": "A DAG address or path to select, with or without the dag:// scheme. The path may be a glob (go/**, {a,b}/*); a ?<dimension>=<key> query selects keys; dag+<type>:// keeps only that type.",
				},
				"type": map[string]any{
					"type":        "string",
					"description": "Keep artifacts of this type, as a GraphQL name (GitRef) or CLI name (git-ref). A module that failed to load is kept too: it could hide artifacts of any type.",
				},
				"keys": map[string]any{
					"type":                 "object",
					"description":          "Select collection keys: {\"<dimension>\": [\"<key>\", ...]}. Same as ?<dimension>=<key> in an address, without URL encoding.",
					"additionalProperties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"view": map[string]any{
					"type":        "string",
					"enum":        []string{findArtifactsViewPaths, findArtifactsViewKeys, findArtifactsViewItems},
					"description": "paths: schema paths with <key> placeholders. items: every fully keyed address, enumerating collection keys. keys: the keys of `dimension`.",
				},
				"dimension": map[string]any{
					"type":        "string",
					"description": "keys view: the dimension (collection) whose keys to list, e.g. \"member\". Implies view: keys.",
				},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		Call: func(ctx context.Context, rawArgs any) (any, error) {
			args, err := parseFindArtifactsArgs(rawArgs)
			if err != nil {
				return nil, err
			}
			return m.findArtifacts(ctx, srv, args)
		},
	})
}

// mayHaveArtifacts reports whether the conversation's scope can be non-empty:
// a module object is bound as tools, or a workspace is bound whose artifacts
// the schema can list (see MCP.scopeWorkspace).
func (m *MCP) mayHaveArtifacts(srv *dagql.Server) bool {
	m.mu.Lock()
	bound := slices.ContainsFunc(m.boundTools, func(b boundTool) bool {
		_, ok := ModuleObjectTypeModule(b.objType)
		return ok
	})
	m.mu.Unlock()
	return bound || m.scopeWorkspace(srv).Self() != nil
}

type findArtifactsArgs struct {
	Address   string
	Type      string
	Keys      map[string][]string
	View      string
	Dimension string
}

// filtered reports whether any filter is set: without one, and without an
// explicit view, FindArtifacts gives the overview.
func (args findArtifactsArgs) filtered() bool {
	return args.Address != "" || args.Type != "" || len(args.Keys) > 0
}

// keyFiltered reports whether a collection key is selected, by the keys
// argument or the address's query. Module and type keys are static.
func (args findArtifactsArgs) keyFiltered(addr *dagaddress.Address) bool {
	for dim := range args.Keys {
		if !artifact.IsStaticDimension(dim) {
			return true
		}
	}
	return addr != nil && slices.ContainsFunc(addr.Query, func(p dagaddress.Pair) bool {
		return p.HasKey && !artifact.IsStaticDimension(p.Dimension)
	})
}

func parseFindArtifactsArgs(raw any) (findArtifactsArgs, error) {
	var args findArtifactsArgs
	vals, ok := raw.(map[string]any)
	if !ok {
		return args, fmt.Errorf("invalid arguments: %T", raw)
	}
	for name, val := range vals {
		switch name {
		case "address", "type", "view", "dimension":
			if val == nil {
				continue
			}
			str, ok := val.(string)
			if !ok {
				return args, fmt.Errorf("%s: expected a string, got %T", name, val)
			}
			str = strings.TrimSpace(str)
			switch name {
			case "address":
				args.Address = str
			case "type":
				args.Type = str
			case "view":
				args.View = str
			case "dimension":
				args.Dimension = str
			}
		case "keys":
			if val == nil {
				continue
			}
			obj, ok := val.(map[string]any)
			if !ok {
				return args, fmt.Errorf("keys: expected an object of dimension to keys, got %T", val)
			}
			args.Keys = map[string][]string{}
			for dim, keys := range obj {
				switch keys := keys.(type) {
				case string:
					args.Keys[dim] = []string{keys}
				case []any:
					list := make([]string, 0, len(keys))
					for _, key := range keys {
						str, ok := key.(string)
						if !ok {
							return args, fmt.Errorf("keys[%q]: expected strings, got %T", dim, key)
						}
						list = append(list, str)
					}
					args.Keys[dim] = list
				default:
					return args, fmt.Errorf("keys[%q]: expected a list of strings, got %T", dim, keys)
				}
			}
		default:
			return args, fmt.Errorf("unknown argument %q", name)
		}
	}
	switch args.View {
	case "", findArtifactsViewPaths, findArtifactsViewItems:
		if args.Dimension != "" && args.View != "" {
			return args, fmt.Errorf("dimension is only used by view %q", findArtifactsViewKeys)
		}
		if args.Dimension != "" {
			args.View = findArtifactsViewKeys
		}
	case findArtifactsViewKeys:
	default:
		return args, fmt.Errorf("unknown view %q: use %q, %q or %q", args.View, findArtifactsViewPaths, findArtifactsViewKeys, findArtifactsViewItems)
	}
	return args, nil
}

// findArtifacts lists the conversation's scope (MCP.Artifacts) through the
// same selection operations as the Artifacts API, so its addresses are the
// ones tool-argument lifting resolves.
func (m *MCP) findArtifacts(ctx context.Context, srv *dagql.Server, args findArtifactsArgs) (string, error) {
	var addr *dagaddress.Address
	var include []string
	if args.Address != "" {
		var err error
		addr, err = dagaddress.Parse(args.Address)
		if err != nil {
			return "", err
		}
		if addr.Absolute {
			return "", fmt.Errorf("%s names a workspace at a commit; FindArtifacts lists this conversation's scope: use a relative address such as dag://%s", args.Address, addr.Path)
		}
		if addr.Path != "" {
			// Load only the workspace modules the path can name, as
			// `dagger list <path>` does.
			include = []string{addr.Path}
		}
	}
	scope, err := m.Artifacts(ctx, srv, include)
	if err != nil {
		return "", err
	}
	if !args.filtered() && args.View == "" {
		return renderArtifactOverview(artifactOverviewOf(scope, boundArtifactTag)), nil
	}

	selection := scope
	if addr != nil {
		selection, err = selection.FilterURI(addr)
		if err != nil {
			return "", err
		}
	}
	if args.Type != "" {
		selection = selection.FilterTypeNames([]string{ArtifactTypeName(args.Type)})
	}
	for _, dim := range slices.Sorted(maps.Keys(args.Keys)) {
		selection = selection.FilterDimensionKeys(dim, args.Keys[dim])
	}

	view := args.View
	if view == "" {
		// As in `dagger list`: a collection key filter only applies to
		// runtime items, so it lists them.
		view = findArtifactsViewPaths
		if args.keyFiltered(addr) {
			view = findArtifactsViewItems
		}
	}
	switch view {
	case findArtifactsViewKeys:
		return findArtifactKeys(ctx, selection, args.Dimension)
	case findArtifactsViewItems:
		expanded, err := selection.Expand(ctx)
		if err != nil {
			return "", err
		}
		rows, err := artifactItemRows(expanded, boundArtifactTag)
		if err != nil {
			return "", err
		}
		return renderArtifactRows(rows), nil
	default:
		schemaSelection, err := selection.SchemaSelection()
		if err != nil {
			return "", err
		}
		rows, err := artifactPathRows(scope, schemaSelection, boundArtifactTag)
		if err != nil {
			return "", err
		}
		return renderArtifactRows(rows), nil
	}
}

// findArtifactKeys lists the keys of one dimension in the selection. Only
// the collection receivers are evaluated, never the items.
func findArtifactKeys(ctx context.Context, selection *Artifacts, name string) (string, error) {
	schemaSelection, err := selection.SchemaSelection()
	if err != nil {
		return "", err
	}
	dims := schemaSelection.DimensionDefinitions()
	if name == "" {
		return "", fmt.Errorf("view %q needs a dimension; dimensions here: %s", findArtifactsViewKeys, dimensionNameList(dims))
	}
	id, err := dims.Resolve(name)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(dims, func(d *ArtifactDimension) bool { return d.Identifier == id })
	if i < 0 {
		return "", fmt.Errorf("unknown dimension %q; dimensions here: %s", name, dimensionNameList(dims))
	}
	keys, err := selection.ExpandDimensionKeys(ctx, id)
	if err != nil {
		return "", err
	}
	return renderArtifactKeys(dims.DisplayName(dims[i]), dims[i], keys), nil
}

func dimensionNameList(dims artifact.Dimensions) string {
	if len(dims) == 0 {
		return "none"
	}
	names := make([]string, 0, len(dims))
	for _, dim := range dims {
		names = append(names, dims.DisplayName(dim))
	}
	return strings.Join(names, ", ")
}

// boundArtifactTag marks an artifact of a bound tool object: it is read from
// the object's live value, not a fresh construction of its module.
func boundArtifactTag(entry *Artifact) string {
	if entry.Workspace.Self() != nil {
		return ""
	}
	if root := entry.BoundRoot(); root != nil {
		return "tool " + root.Type().Name() + ", live"
	}
	return "tool"
}

// artifactRow is one line of FindArtifacts output.
type artifactRow struct {
	// Address is the DAG address, with a <key> placeholder for each dimension
	// a schema path still needs.
	Address     string
	Description string
	LoadError   string
	// Tag marks where the artifact comes from, e.g. "tool Staff, live".
	Tag string
	// Needs lists the dimensions the placeholders stand for.
	Needs []artifactRowDimension
}

// artifactRowDimension is a dimension a schema path needs a key for.
type artifactRowDimension struct {
	// Name is the query name used in the row's address.
	Name string
	// Lookup is the name that selects the dimension in the whole scope, for
	// the keys view.
	Lookup         string
	KeyName        string
	KeyDescription string
}

func (d artifactRowDimension) placeholder() string {
	key := d.KeyName
	if key == "" {
		key = "key"
	}
	return d.Name + "=<" + key + ">"
}

// artifactPathRows renders the selection's path definitions (as
// Artifacts.pathDefinitions(typeAssertion: true) does), each with the
// dimensions it needs. Dimension names are chosen within the path's own scope,
// as Artifact.uri names them, so a filled-in address resolves.
func artifactPathRows(scope, selection *Artifacts, tag func(*Artifact) string) ([]artifactRow, error) {
	opts := ArtifactURIOpts{TypeAssertion: true}
	paths, err := selection.PathDefinitions(opts)
	if err != nil {
		return nil, err
	}
	entries := map[string]*Artifact{}
	for _, entry := range selection.Entries {
		uri, err := entry.URI(opts)
		if err != nil {
			return nil, err
		}
		if _, ok := entries[uri]; !ok {
			entries[uri] = entry
		}
	}
	scopeDims := scope.DimensionDefinitions()
	rows := make([]artifactRow, 0, len(paths))
	for _, path := range paths {
		entry := entries[path.URI]
		row := artifactRow{Address: path.URI, Description: path.Description, LoadError: path.LoadError, Tag: tag(entry)}
		pathDims := scope.FilterPath(entry.Path).DimensionDefinitions()
		defs := entry.DimensionDefinitions()
		var query []string
		for _, id := range path.Dimensions {
			if artifact.IsStaticDimension(id) {
				continue
			}
			i := slices.IndexFunc(defs, func(d *ArtifactDimension) bool { return d.Identifier == id })
			if i < 0 {
				continue
			}
			def := defs[i]
			need := artifactRowDimension{
				Name:           pathDims.DisplayName(def),
				Lookup:         scopeDims.DisplayName(def),
				KeyName:        def.KeyName,
				KeyDescription: def.KeyDescription,
			}
			row.Needs = append(row.Needs, need)
			query = append(query, need.placeholder())
		}
		if len(query) > 0 {
			row.Address += "?" + strings.Join(query, "&")
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// artifactItemRows renders expanded artifacts by their complete addresses,
// as Artifact.uri(typeAssertion: true) prints them.
func artifactItemRows(expanded *Artifacts, tag func(*Artifact) string) ([]artifactRow, error) {
	rows := make([]artifactRow, 0, len(expanded.Entries))
	for _, entry := range expanded.Entries {
		uri, err := entry.URI(ArtifactURIOpts{DimensionKeys: true, TypeAssertion: true})
		if err != nil {
			return nil, err
		}
		row := artifactRow{Address: uri, Description: entry.Description(), Tag: tag(entry)}
		if entry.LoadFailure != nil {
			row.LoadError = entry.LoadFailure.Message
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// renderArtifactRows prints one row per artifact, then the dimensions the
// rows' placeholders stand for.
func renderArtifactRows(rows []artifactRow) string {
	if len(rows) == 0 {
		return "No artifacts match. Call " + findArtifactsToolName + " with no arguments for an overview of the scope."
	}
	var out strings.Builder
	var needs []artifactRowDimension
	for _, row := range rows {
		out.WriteString(renderArtifactRow(row))
		out.WriteString("\n")
		for _, need := range row.Needs {
			if !slices.Contains(needs, need) {
				needs = append(needs, need)
			}
		}
	}
	if len(needs) > 0 {
		out.WriteString("\nReplace each <placeholder> with a key (view: \"items\" lists every fully keyed address):\n")
		for _, need := range needs {
			out.WriteString("  ")
			out.WriteString(need.placeholder())
			if description := firstLine(need.KeyDescription); description != "" {
				out.WriteString(" — ")
				out.WriteString(description)
			}
			fmt.Fprintf(&out, " (keys: %s(dimension: %q))\n", findArtifactsToolName, need.Lookup)
		}
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func renderArtifactRow(row artifactRow) string {
	line := row.Address
	if row.LoadError != "" {
		line += " — LOAD ERROR: " + strings.ReplaceAll(strings.TrimSpace(row.LoadError), "\n", "\n    ")
	} else if description := firstLine(row.Description); description != "" {
		line += " — " + description
	}
	if row.Tag != "" {
		line += " [" + row.Tag + "]"
	}
	return line
}

func renderArtifactKeys(name string, dim *ArtifactDimension, keys []string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Keys of %s", name)
	var about []string
	if dim.ItemType != "" {
		about = append(about, dim.ItemType)
	}
	if dim.KeyName != "" {
		keyed := "by " + dim.KeyName
		if description := firstLine(dim.KeyDescription); description != "" {
			keyed += ": " + description
		}
		about = append(about, keyed)
	}
	if len(about) > 0 {
		out.WriteString(" (" + strings.Join(about, ", ") + ")")
	}
	if len(keys) == 0 {
		out.WriteString(": none in this selection.")
		return out.String()
	}
	out.WriteString(":\n")
	for _, key := range keys {
		out.WriteString(key)
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "Select one with ?%s=<key> in an address, or keys: {%q: [\"<key>\"]}.", name, name)
	return out.String()
}

// artifactOverview is FindArtifacts without arguments: what `dagger list`
// offers to list.
type artifactOverview struct {
	Types       []artifactOverviewType
	Collections []artifactOverviewCollection
	LoadErrors  []artifactRow
}

type artifactOverviewType struct {
	Name    string
	Count   int
	Modules []string
}

type artifactOverviewCollection struct {
	Name           string
	ItemType       string
	CollectionType string
	KeyName        string
	KeyDescription string
}

// artifactOverviewOf counts the scope's schema paths per type, with the
// modules (in CLI case, as addresses spell them) that have them. It reads no
// runtime values.
func artifactOverviewOf(scope *Artifacts, tag func(*Artifact) string) artifactOverview {
	var overview artifactOverview
	counts := map[string]int{}
	modules := map[string][]string{}
	for _, entry := range scope.Entries {
		module := ArtifactTypeName(entry.ModuleName)
		if entry.LoadFailure != nil {
			row := artifactRow{Description: entry.Description(), LoadError: entry.LoadFailure.Message, Tag: tag(entry)}
			row.Address, _ = entry.URI(ArtifactURIOpts{TypeAssertion: true})
			overview.LoadErrors = append(overview.LoadErrors, row)
			continue
		}
		if tag(entry) != "" {
			module += " [tool]"
		}
		counts[entry.TypeName]++
		if !slices.Contains(modules[entry.TypeName], module) {
			modules[entry.TypeName] = append(modules[entry.TypeName], module)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		mods := modules[name]
		slices.Sort(mods)
		overview.Types = append(overview.Types, artifactOverviewType{Name: name, Count: counts[name], Modules: mods})
	}
	dims := scope.DimensionDefinitions()
	for _, dim := range dims {
		if artifact.IsStaticDimension(dim.Identifier) {
			continue
		}
		overview.Collections = append(overview.Collections, artifactOverviewCollection{
			Name: dims.DisplayName(dim), ItemType: dim.ItemType, CollectionType: dim.CollectionType,
			KeyName: dim.KeyName, KeyDescription: dim.KeyDescription,
		})
	}
	return overview
}

func renderArtifactOverview(overview artifactOverview) string {
	if len(overview.Types) == 0 && len(overview.LoadErrors) == 0 {
		return "No artifacts in scope: neither the workspace's modules nor your bound tool modules have any."
	}
	var out strings.Builder
	if len(overview.Types) > 0 {
		out.WriteString("Artifact types in scope (schema paths: modules):\n")
		for _, typ := range overview.Types {
			fmt.Fprintf(&out, "  %s (%d): %s\n", typ.Name, typ.Count, strings.Join(typ.Modules, ", "))
		}
	}
	if len(overview.Collections) > 0 {
		out.WriteString("\nCollections (dimensions whose keys address items):\n")
		for _, coll := range overview.Collections {
			fmt.Fprintf(&out, "  %s: %s items of %s", coll.Name, coll.ItemType, coll.CollectionType)
			if coll.KeyName != "" {
				fmt.Fprintf(&out, ", keyed by %s", coll.KeyName)
				if description := firstLine(coll.KeyDescription); description != "" {
					out.WriteString(" (" + strings.TrimRight(description, ".") + ")")
				}
			}
			out.WriteString("\n")
		}
	}
	if len(overview.LoadErrors) > 0 {
		out.WriteString("\nModules that failed to load (they may hide artifacts of any type):\n")
		for _, row := range overview.LoadErrors {
			out.WriteString("  " + renderArtifactRow(row) + "\n")
		}
	}
	out.WriteString("\nNarrow with type (e.g. type: \"Check\"), address (e.g. address: \"<module>/**\") or keys. ")
	out.WriteString("view: \"items\" lists every fully keyed address; dimension: \"<collection>\" lists its keys.")
	return out.String()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
