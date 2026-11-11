package daggercmd

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type artifactPreviewEntry struct {
	flattening.Artifact
	Description string
	Items       []struct {
		Keys        []string
		Description string
	}
}

// PreviewArtifacts renders a captured schema and its observed items without
// connecting to an engine. Arguments use the check, generate, or list syntax.
func PreviewArtifacts(input io.Reader, output, diagnostics io.Writer, args []string) error {
	var entries []artifactPreviewEntry
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return fmt.Errorf("read artifact preview: %w", err)
	}
	if len(args) == 0 {
		return fmt.Errorf("specify check -l, generate -l, or list TYPE")
	}
	schema := make([]flattening.Artifact, len(entries))
	for i, e := range entries {
		schema[i] = e.Artifact
	}
	plan := flattening.New(schema)
	coordinates := make([][]flattening.Row, len(entries))
	for i, entry := range entries {
		for _, item := range entry.Items {
			row, err := plan.Flatten(entry.Artifact, item.Keys...)
			if err != nil {
				return err
			}
			coordinates[i] = append(coordinates[i], row)
		}
	}
	definitions := make(artifact.Dimensions, 0)
	for _, d := range plan.Dimensions() {
		definitions = append(definitions, (*artifact.Dimension)(d))
	}
	var paths []artifactListPath
	var fullPaths artifact.Paths
	entryDimensions := make([][]string, len(entries))
	for i, e := range entries {
		var parent *flattening.Dimension
		for _, c := range e.Collections {
			parent = c.Dimension(e.Module, parent, nil)
			entryDimensions[i] = append(entryDimensions[i], parent.Identifier)
		}
		fullPaths = append(fullPaths, artifact.Path{Type: "type:" + e.Type, Key: strings.Join(e.Path, "/"), Module: e.Module, Dimensions: entryDimensions[i]})
		dimensions := []string{}
		for _, d := range flattening.ArtifactDimensions(e.Module, e.Type, nil) {
			dimensions = append(dimensions, d.Identifier)
		}
		dimensions = append(dimensions, entryDimensions[i]...)
		addr := dagaddress.Address{Types: []string{cliName(e.Type)}, Query: []dagaddress.Pair{{Dimension: definitions.DisplayName(&artifact.Dimension{Identifier: "type:" + e.Type, Name: cliName(e.Type), QualifiedName: "artifact-" + cliName(e.Type)}), Key: strings.Join(e.Path, "/"), HasKey: true}}}
		paths = append(paths, artifactListPath{URI: addr.String(), ModuleName: e.Module, Dimensions: dimensions})
	}
	fullPaths = artifact.UnionPaths(fullPaths)
	cmd, collection, remaining, err := artifactPreviewCommand(args, definitions, paths)
	if err != nil {
		return err
	}
	cmd.SetOut(output)
	cmd.SetErr(diagnostics)
	if err := cmd.ParseFlags(remaining); err != nil {
		return err
	}
	unsupported := ""
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if len(f.Annotations[artifactDimensionFlag]) == 0 && !slices.Contains([]string{"list", "all", "format"}, f.Name) {
			unsupported = f.Name
		}
	})
	if unsupported != "" {
		return fmt.Errorf("unsupported preview option --%s", unsupported)
	}
	if cmd.Name() != "list" {
		listed, _ := cmd.Flags().GetBool("list")
		if !listed {
			return fmt.Errorf("preview requires --list")
		}
	}
	if err := validateArtifactListFormat(cmd.Flag("format").Value.String()); err != nil {
		return err
	}
	filters := artifactKeyFlags(cmd)
	addresses, err := parseArtifactAddresses(cmd.Flags().Args())
	if err != nil {
		return err
	}
	if len(addresses) > 1 {
		return fmt.Errorf("preview supports one path selector")
	}
	var address *dagaddress.Address
	if len(addresses) == 1 {
		address = addresses[0]
		if address.Absolute {
			return fmt.Errorf("preview does not contain workspace addresses")
		}
		filters = append(filters, address.Query...)
	}
	for i := range filters {
		filters[i].Dimension, err = definitions.Resolve(filters[i].Dimension)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(definitions, func(d *artifact.Dimension) bool { return d.Identifier == filters[i].Dimension }) {
			return fmt.Errorf("unknown dimension %q", filters[i].Dimension)
		}
	}
	all, _ := cmd.Flags().GetBool("all")
	expanded := all || slices.ContainsFunc(filters, func(p dagaddress.Pair) bool { return p.HasKey && !artifact.IsStaticDimension(p.Dimension) })
	index, err := newArtifactNameIndex(paths)
	if err != nil {
		return err
	}
	var rows []listedArtifact
	for i, e := range entries {
		types := commandArtifactTypes(cmd)
		if typ := cmd.Annotations[artifactListType]; typ != "" {
			types = []string{typ}
		}
		if len(types) > 0 && !slices.Contains(types, e.Type) {
			continue
		}
		if address != nil && len(address.Types) > 0 && !slices.Contains(address.Types, cliName(e.Type)) {
			continue
		}
		if collection != "" && !slices.ContainsFunc(definitions, func(d *artifact.Dimension) bool {
			return d.CollectionType == collection && d.ItemType == e.Type && len(entryDimensions[i]) > 0 && entryDimensions[i][len(entryDimensions[i])-1] == d.Identifier
		}) {
			continue
		}
		if address != nil && address.Path != "" {
			match, err := doublestar.PathMatch(address.Path, strings.Join(e.Path, "/"))
			if err != nil {
				return err
			}
			child, err := doublestar.PathMatch(strings.TrimSuffix(address.Path, "/")+"/**", strings.Join(e.Path, "/"))
			if err != nil {
				return err
			}
			if !match && !child {
				continue
			}
		}
		itemRows := []listedArtifact{}
		if expanded {
			for j, observed := range e.Items {
				flat := coordinates[i][j]
				row := listedArtifact{ModuleName: e.Module, Path: e.Path, Description: e.Description, CollectionItem: collection != ""}
				if observed.Description != "" {
					row.Description = observed.Description
				}
				for _, key := range flat.Coordinates {
					row.DimensionKeys = append(row.DimensionKeys, struct{ Dimension, Key string }{key.Dimension, key.Key})
				}
				row.URI = artifactPreviewLink(e.Artifact, flat, entryDimensions[i], fullPaths, definitions)
				itemRows = append(itemRows, row)
			}
		} else {
			row := listedArtifact{URI: paths[i].URI, ModuleName: e.Module, Path: e.Path, Description: e.Description, CollectionItem: collection != "", Presence: entryDimensions[i]}
			for _, key := range flattening.Coordinates(e.Module, e.Path, e.Type, nil) {
				row.DimensionKeys = append(row.DimensionKeys, struct{ Dimension, Key string }{key.Dimension, key.Key})
			}
			itemRows = append(itemRows, row)
		}
		for _, row := range itemRows {
			if artifactPreviewMatches(row, filters, index) {
				rows = append(rows, row)
			}
		}
	}
	if len(rows) == 0 {
		return fmt.Errorf("no captured artifacts match the selection")
	}
	groups := map[string][]listedArtifact{}
	for _, row := range rows {
		scheme, _, _ := strings.Cut(row.URI, "://")
		key := row.fullPath() + "\x00" + scheme
		groups[key] = append(groups[key], row)
	}
	var rendered []listedArtifact
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		for _, group := range artifactListRows(groups[key], all) {
			row := group[0]
			row.DimensionKeys = nil
			for _, key := range listedArtifactKeys(group) {
				row.DimensionKeys = append(row.DimensionKeys, struct{ Dimension, Key string }{key.Dimension, key.Key})
			}
			if len(group) > 1 {
				row.URI, err = collapsedArtifactURI(row)
				if err != nil {
					return err
				}
			}
			rendered = append(rendered, row)
		}
	}
	names, err := prepareArtifactOutputWithSchema(cmd, rendered, definitions, paths)
	if err != nil {
		return err
	}
	if err := writeArtifactList(cmd, rendered, names); err != nil {
		return err
	}
	if len(rendered) < len(rows) || slices.ContainsFunc(rendered, func(r listedArtifact) bool { return len(r.Presence) > 0 }) {
		_, err = fmt.Fprintln(diagnostics, "# Use --all to expand collections and list each item.")
	}
	return err
}

func artifactPreviewCommand(args []string, definitions artifact.Dimensions, paths []artifactListPath) (*cobra.Command, string, []string, error) {
	root := &cobra.Command{Use: "dagger"}
	for _, original := range []*cobra.Command{checksCmd, generateCmd, startCmd, shellCmd, agentCmd, listCmd} {
		cmd := &cobra.Command{Use: original.Name()}
		registerArtifactListFlags(cmd)
		cmd.Flags().StringP("format", "f", "table", "Output format")
		if cmd.Name() != "list" {
			cmd.Flags().BoolP("list", "l", false, "List artifacts")
		}
		// Reserve the real command's other flag names without sharing its
		// mutable values. Preview rejects those options when explicitly set.
		original.Flags().VisitAll(func(flag *pflag.Flag) {
			if cmd.Flag(flag.Name) == nil {
				cmd.Flags().Bool(flag.Name, false, "Unsupported preview option")
			}
		})
		root.AddCommand(cmd)
	}
	var cmd *cobra.Command
	for _, candidate := range root.Commands() {
		if candidate.Name() == args[0] {
			cmd = candidate
		}
	}
	if cmd == nil || !slices.Contains([]string{"check", "generate", "list"}, cmd.Name()) {
		return nil, "", nil, fmt.Errorf("unsupported preview command %q", args[0])
	}
	remaining := args[1:]
	collection := ""
	if cmd.Name() == "list" {
		if len(remaining) == 0 || strings.HasPrefix(remaining[0], "-") {
			return nil, "", nil, fmt.Errorf("specify a list type or collection")
		}
		var types []string
		collections := map[string]bool{}
		for _, d := range definitions {
			if d.Kind == "TYPE" {
				types = append(types, d.ItemType)
			}
			if d.CollectionType != "" {
				types = append(types, d.CollectionType)
				collections[d.CollectionType] = true
			}
		}
		typ := artifactTypeCommands(types, collections)[remaining[0]]
		if typ == "" {
			return nil, "", nil, fmt.Errorf("unknown captured type or collection %q", remaining[0])
		}
		cmd.Annotations = map[string]string{}
		if collections[typ] {
			collection = typ
			cmd.Annotations[artifactListCollection] = typ
		} else {
			cmd.Annotations[artifactListType] = typ
		}
		remaining = remaining[1:]
	}
	selectors := artifactSelectorDimensions(definitions)
	registerArtifactDimensionHelp(cmd, selectors, selectors)
	registerArtifactModuleFlags(cmd, definitions, definitions, paths)
	return cmd, collection, remaining, nil
}

func artifactPreviewMatches(row listedArtifact, filters []dagaddress.Pair, index artifactNameIndex) bool {
	dimensions := map[string][]dagaddress.Pair{}
	for _, filter := range filters {
		dimensions[filter.Dimension] = append(dimensions[filter.Dimension], filter)
	}
	for dimension, choices := range dimensions {
		matched := false
		for _, filter := range choices {
			for _, key := range row.DimensionKeys {
				if key.Dimension != dimension {
					continue
				}
				if !filter.HasKey || key.Key == filter.Key {
					matched = true
				}
				if strings.HasPrefix(dimension, "type:") && filter.HasKey && slices.Contains(artifact.ResolveTypeKey(index.candidatePaths(dimension, filters), filter.Key), key.Key) {
					matched = true
				}
			}
			if !filter.HasKey && slices.Contains(row.Presence, dimension) {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func artifactPreviewLink(a flattening.Artifact, row flattening.Row, dimensions []string, paths artifact.Paths, definitions artifact.Dimensions) string {
	target := artifact.Path{Type: "type:" + a.Type, Module: a.Module, Key: strings.Join(a.Path, "/"), Dimensions: dimensions}
	name := func(value string) string { return cliName(strings.TrimPrefix(value, "type:")) }
	link := artifact.PlanLink(paths, target, true, true, name)
	addr := dagaddress.Address{Types: []string{cliName(a.Type)}}
	display := func(id string) string {
		for _, d := range definitions {
			if d.Identifier == id {
				return definitions.DisplayName(d)
			}
		}
		return id
	}
	typeKey := row.TypeKey
	if link != nil {
		typeKey = link.TypeKey
		if link.Module {
			addr.Query = append(addr.Query, dagaddress.Pair{Dimension: "module", Key: a.Module, HasKey: true})
		}
	}
	if typeKey != "" {
		addr.Query = append(addr.Query, dagaddress.Pair{Dimension: display(target.Type), Key: typeKey, HasKey: true})
	}
	for _, key := range row.Coordinates {
		if !artifact.IsStaticDimension(key.Dimension) {
			addr.Query = append(addr.Query, dagaddress.Pair{Dimension: display(key.Dimension), Key: key.Key, HasKey: true})
		}
	}
	return addr.String()
}
