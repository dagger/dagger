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
	LocalPath   []string
	Description string
	Items       []struct {
		Keys        []string
		Description string
	}
}

type artifactPreviewCapture struct {
	entries        []artifactPreviewEntry
	fields         []flattening.Field
	coordinates    [][][]flattening.Coordinate
	addressPaths   artifact.Paths
	addressesIndex *artifact.AddressIndex
	definitions    artifact.Dimensions
	linkNames      map[string]string
	paths          []artifactListPath
}

type artifactPreviewSelection struct {
	cmd        *cobra.Command
	collection string
	address    *dagaddress.Address
	filters    []dagaddress.Pair
	expanded   bool
}

func PreviewArtifacts(input io.Reader, output, diagnostics io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("specify check -l, generate -l, or list TYPE")
	}
	capture, err := readArtifactPreview(input)
	if err != nil {
		return err
	}
	selection, err := capture.parseSelection(args)
	if err != nil {
		return err
	}
	selection.cmd.SetOut(output)
	selection.cmd.SetErr(diagnostics)
	rows, err := capture.selectRows(selection)
	if err != nil {
		return err
	}
	return capture.render(selection.cmd, diagnostics, rows, selection.expanded)
}

func readArtifactPreview(input io.Reader) (*artifactPreviewCapture, error) {
	var entries []artifactPreviewEntry
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("read artifact preview: %w", err)
	}
	fields := make([]flattening.Field, len(entries))
	addressPaths := make(artifact.Paths, len(entries))
	for i, e := range entries {
		if e.LocalPath == nil {
			return nil, fmt.Errorf("artifact %s needs an explicit LocalPath; use [] for a root", strings.Join(e.Path, "/"))
		}
		fields[i] = flattening.Field{Module: e.Module, Type: e.Type, LocalPath: e.LocalPath, Collections: e.Collections}
		addressPaths[i] = artifact.Path{Module: e.Module, Type: "type:" + cliName(e.Type), Key: strings.Join(e.Path, "/")}
		for _, collection := range e.Collections {
			addressPaths[i].Dimensions = append(addressPaths[i].Dimensions, collection.Type)
		}
	}
	schema, err := flattening.NewSchema(fields)
	if err != nil {
		return nil, err
	}
	coordinates := make([][][]flattening.Coordinate, len(entries))
	for i, entry := range entries {
		for _, item := range entry.Items {
			canonical, err := schema.Coordinates(fields[i], item.Keys...)
			if err != nil {
				return nil, err
			}
			coordinates[i] = append(coordinates[i], canonical)
		}
	}
	var addressRows []artifact.AddressRow
	for i, entry := range entries {
		for _, row := range coordinates[i] {
			addressRows = append(addressRows, artifact.AddressRow{Type: cliName(entry.Type), Coordinates: row})
		}
	}
	addressesIndex := artifact.NewAddressIndex(addressRows, true)
	definitions := make(artifact.Dimensions, 0)
	for _, entry := range entries {
		id := "type:" + entry.Type
		if !slices.ContainsFunc(definitions, func(d *artifact.Dimension) bool { return d.Identifier == id }) {
			definitions = append(definitions, &artifact.Dimension{
				Identifier: id, Kind: "TYPE", Name: cliName(entry.Type),
				QualifiedName: "artifact-" + cliName(entry.Type), ItemType: entry.Type,
			})
		}
	}
	canonicalDefinitions := make(artifact.Dimensions, 0)
	for _, definition := range schema.Dimensions() {
		canonicalDefinitions = append(canonicalDefinitions, (*artifact.Dimension)(definition))
		definitions = append(definitions, (*artifact.Dimension)(definition))
	}
	linkNames := make(map[string]string, len(canonicalDefinitions))
	for _, definition := range canonicalDefinitions {
		linkNames[definition.Identifier] = canonicalDefinitions.DisplayName(definition)
	}
	var paths []artifactListPath
	for i, e := range entries {
		dimensions := []string{}
		for _, d := range flattening.ArtifactDimensions(e.Module, e.Type, nil) {
			dimensions = append(dimensions, d.Identifier)
		}
		dimensions = append(dimensions, addressPaths[i].Dimensions...)
		addr := dagaddress.Address{Types: []string{cliName(e.Type)}, Query: []dagaddress.Pair{{Dimension: definitions.DisplayName(&artifact.Dimension{Identifier: "type:" + e.Type, Name: cliName(e.Type), QualifiedName: "artifact-" + cliName(e.Type)}), Key: strings.Join(e.Path, "/"), HasKey: true}}}
		path := artifactListPath{URI: addr.String(), ModuleName: e.Module, Dimensions: dimensions}
		for _, coordinate := range flattening.CanonicalCoordinates(e.Module, fields[i].FieldPath(), nil) {
			path.Dimensions = append(path.Dimensions, coordinate.Dimension)
			if coordinate.Dimension == flattening.NameDimension {
				path.Name = coordinate.Key
			}
		}
		paths = append(paths, path)
	}
	return &artifactPreviewCapture{
		entries: entries, fields: fields, coordinates: coordinates,
		addressPaths: addressPaths, addressesIndex: addressesIndex,
		definitions: definitions, linkNames: linkNames, paths: paths,
	}, nil
}

func (capture *artifactPreviewCapture) parseSelection(args []string) (*artifactPreviewSelection, error) {
	cmd, collection, remaining, err := artifactPreviewCommand(args, capture.definitions, capture.paths)
	if err != nil {
		return nil, err
	}
	if err := cmd.ParseFlags(remaining); err != nil {
		return nil, err
	}
	unsupported := ""
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if len(f.Annotations[artifactDimensionFlag]) == 0 && !slices.Contains([]string{"list", "all", "format"}, f.Name) {
			unsupported = f.Name
		}
	})
	if unsupported != "" {
		return nil, fmt.Errorf("unsupported preview option --%s", unsupported)
	}
	if cmd.Name() != "list" {
		listed, _ := cmd.Flags().GetBool("list")
		if !listed {
			return nil, fmt.Errorf("preview requires --list")
		}
	}
	if err := validateArtifactListFormat(cmd.Flag("format").Value.String()); err != nil {
		return nil, err
	}
	filters := artifactKeyFlags(cmd)
	addresses, err := parseArtifactAddresses(cmd.Flags().Args())
	if err != nil {
		return nil, err
	}
	if len(addresses) > 1 {
		return nil, fmt.Errorf("preview supports one path selector")
	}
	var address *dagaddress.Address
	if len(addresses) == 1 {
		address = addresses[0]
		if address.Absolute {
			return nil, fmt.Errorf("preview does not contain workspace addresses")
		}
		filters = append(filters, address.Query...)
	}
	for i := range filters {
		filters[i].Dimension, err = capture.definitions.Resolve(filters[i].Dimension)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(capture.definitions, func(d *artifact.Dimension) bool { return d.Identifier == filters[i].Dimension }) {
			return nil, fmt.Errorf("unknown dimension %q", filters[i].Dimension)
		}
	}
	all, _ := cmd.Flags().GetBool("all")
	expanded := all || collection != "" || slices.ContainsFunc(filters, func(p dagaddress.Pair) bool { return p.HasKey && !artifact.IsStaticDimension(p.Dimension) })
	return &artifactPreviewSelection{cmd: cmd, collection: collection, address: address, filters: filters, expanded: expanded}, nil
}

func (capture *artifactPreviewCapture) selectRows(selection *artifactPreviewSelection) ([]listedArtifact, error) {
	index, err := newArtifactNameIndex(capture.paths)
	if err != nil {
		return nil, err
	}
	var rows []listedArtifact
	for i, entry := range capture.entries {
		matched, err := selection.matchesEntry(entry, capture.addressPaths[i].Dimensions, capture.definitions)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		for _, row := range capture.projectEntry(i, selection) {
			if artifactPreviewMatches(row, selection.filters, index) {
				rows = append(rows, row)
			}
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no captured artifacts match the selection")
	}
	return rows, nil
}

func (selection *artifactPreviewSelection) matchesEntry(entry artifactPreviewEntry, dimensions []string, definitions artifact.Dimensions) (bool, error) {
	types := commandArtifactTypes(selection.cmd)
	if typ := selection.cmd.Annotations[artifactListType]; typ != "" {
		types = []string{typ}
	}
	if len(types) > 0 && !slices.Contains(types, entry.Type) {
		return false, nil
	}
	if selection.address != nil && len(selection.address.Types) > 0 && !slices.Contains(selection.address.Types, cliName(entry.Type)) {
		return false, nil
	}
	if selection.collection != "" && !slices.ContainsFunc(definitions, func(d *artifact.Dimension) bool {
		return d.CollectionType == selection.collection && d.ItemType == entry.Type && len(dimensions) > 0 && dimensions[len(dimensions)-1] == d.Identifier
	}) {
		return false, nil
	}
	if selection.address != nil && selection.address.Path != "" {
		match, err := doublestar.PathMatch(selection.address.Path, strings.Join(entry.Path, "/"))
		if err != nil {
			return false, err
		}
		child, err := doublestar.PathMatch(strings.TrimSuffix(selection.address.Path, "/")+"/**", strings.Join(entry.Path, "/"))
		if err != nil {
			return false, err
		}
		if !match && !child {
			return false, nil
		}
	}
	return true, nil
}

func (capture *artifactPreviewCapture) projectEntry(i int, selection *artifactPreviewSelection) []listedArtifact {
	e := capture.entries[i]
	itemRows := []listedArtifact{}
	if selection.expanded {
		for j, observed := range e.Items {
			row := listedArtifact{ModuleName: e.Module, Path: e.Path, Description: e.Description, CollectionItem: selection.collection != ""}
			if observed.Description != "" {
				row.Description = observed.Description
			}
			for _, key := range flattening.Coordinates(e.Module, e.Path, e.Type, capture.coordinates[i][j]) {
				row.DimensionKeys = append(row.DimensionKeys, struct{ Dimension, Key string }{key.Dimension, key.Key})
			}
			row.URI = artifactPreviewLink(e, capture.coordinates[i][j], capture.addressesIndex, capture.linkNames, capture.addressPaths)
			setArtifactPreviewCoordinates(&row, capture.coordinates[i][j])
			itemRows = append(itemRows, row)
		}
	} else {
		schemaURI := dagaddress.Address{Types: []string{cliName(e.Type)}, Path: strings.Join(e.Path, "/")}
		if artifact.PathNeedsModule(capture.addressPaths, capture.addressPaths[i], true) {
			schemaURI.Query = append(schemaURI.Query, artifactPreviewModuleSelector(e.Module))
		}
		for _, dimension := range artifact.PathPresenceDimensions(capture.addressPaths, capture.addressPaths[i], true) {
			schemaURI.Query = append(schemaURI.Query, dagaddress.Pair{Dimension: dimension})
		}
		for _, dimension := range artifact.PathAbsentDimensions(capture.addressPaths, capture.addressPaths[i], true) {
			schemaURI.Query = append(schemaURI.Query, dagaddress.Pair{Dimension: dimension, Absent: true})
		}
		row := listedArtifact{URI: schemaURI.String(), ModuleName: e.Module, Path: e.Path, Description: e.Description, CollectionItem: selection.collection != "", Presence: capture.addressPaths[i].Dimensions}
		for _, key := range flattening.Coordinates(e.Module, e.Path, e.Type, nil) {
			row.DimensionKeys = append(row.DimensionKeys, struct{ Dimension, Key string }{key.Dimension, key.Key})
		}
		setArtifactPreviewCoordinates(&row, flattening.CanonicalCoordinates(e.Module, capture.fields[i].FieldPath(), nil))
		itemRows = append(itemRows, row)
	}
	return itemRows
}

func (capture *artifactPreviewCapture) render(cmd *cobra.Command, diagnostics io.Writer, rows []listedArtifact, expanded bool) error {
	groups := map[string][]listedArtifact{}
	for _, row := range rows {
		scheme, _, _ := strings.Cut(row.URI, "://")
		key := row.fullPath() + "\x00" + scheme
		groups[key] = append(groups[key], row)
	}
	var rendered []listedArtifact
	var err error
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		for _, group := range artifactListRows(groups[key], expanded) {
			row := group[0]
			row.Coordinates = listedArtifactCoordinates(group)
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
	names, err := prepareArtifactOutputWithSchema(cmd, rendered, capture.definitions, capture.paths)
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

const artifactPreviewListCommand = "dagger.io/preview-list-command"

func setArtifactPreviewCoordinates(row *listedArtifact, coordinates []flattening.Coordinate) {
	for _, coordinate := range coordinates {
		row.Coordinates = append(row.Coordinates, struct{ Dimension, Key string }{coordinate.Dimension, coordinate.Key})
		if coordinate.Dimension == flattening.NameDimension {
			row.Name = coordinate.Key
		}
	}
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
		cmd.Annotations = map[string]string{artifactPreviewListCommand: remaining[0]}
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
	positive := map[string][]dagaddress.Pair{}
	present := func(dimension string) bool {
		if dimension == flattening.NameDimension && row.Name != "" {
			return true
		}
		if slices.Contains(row.Presence, dimension) {
			return true
		}
		keys := row.Coordinates
		if artifact.IsStaticDimension(dimension) && dimension != flattening.NameDimension {
			keys = row.DimensionKeys
		}
		return slices.ContainsFunc(keys, func(key struct{ Dimension, Key string }) bool { return key.Dimension == dimension })
	}
	for _, filter := range filters {
		if filter.Absent {
			if present(filter.Dimension) {
				return false
			}
		} else {
			positive[filter.Dimension] = append(positive[filter.Dimension], filter)
		}
	}
	for dimension, choices := range positive {
		matched := false
		for _, filter := range choices {
			if !filter.HasKey && present(dimension) {
				matched = true
			}
			if dimension == flattening.NameDimension && filter.HasKey && row.Name == filter.Key {
				matched = true
			}
			keys := row.Coordinates
			if artifact.IsStaticDimension(dimension) && dimension != flattening.NameDimension {
				keys = row.DimensionKeys
			}
			for _, key := range keys {
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
		}
		if !matched {
			return false
		}
	}
	return true
}

func artifactPreviewLink(entry artifactPreviewEntry, coordinates []flattening.Coordinate, index *artifact.AddressIndex, names map[string]string, paths artifact.Paths) string {
	target := artifact.AddressRow{Type: cliName(entry.Type), Coordinates: coordinates}
	keys, ok := index.Selectors(target)
	addr := dagaddress.Address{Types: []string{cliName(entry.Type)}}
	if !ok {
		addr.Path = strings.Join(entry.Path, "/")
		keys = nil
		for _, coordinate := range target.Coordinates {
			if !artifact.IsStaticDimension(coordinate.Dimension) {
				keys = append(keys, dagaddress.Pair{Dimension: coordinate.Dimension, Key: coordinate.Key, HasKey: true})
			}
		}
		path := artifact.Path{Module: entry.Module, Type: "type:" + cliName(entry.Type), Key: strings.Join(entry.Path, "/")}
		for _, collection := range entry.Collections {
			path.Dimensions = append(path.Dimensions, collection.Type)
		}
		if artifact.PathNeedsModule(paths, path, true) {
			keys = append(keys, artifactPreviewModuleSelector(entry.Module))
		}
		for _, dimension := range artifact.PathAbsentDimensions(paths, path, true) {
			keys = append(keys, dagaddress.Pair{Dimension: dimension, Absent: true})
		}
	}
	for _, key := range keys {
		name := names[key.Dimension]
		if name == "" || !ok && key.Absent {
			name = key.Dimension
		}
		addr.Query = append(addr.Query, dagaddress.Pair{Dimension: name, Key: key.Key, HasKey: key.HasKey, Absent: key.Absent})
	}
	return addr.String()
}

func artifactPreviewModuleSelector(module string) dagaddress.Pair {
	return dagaddress.Pair{Dimension: artifact.ModuleDimension, Key: module, HasKey: module != "", Absent: module == ""}
}
