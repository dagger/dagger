package daggercmd

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/syntax"
)

func validateArtifactListFormat(format string) error {
	switch format {
	case "table", "link", "cli":
		return nil
	default:
		return fmt.Errorf("unknown format %q; use table, link, or cli", format)
	}
}

// uniqueArtifactList gives table output and trace links the same row order.
func uniqueArtifactList(items []listedArtifact) []listedArtifact {
	slices.SortFunc(items, func(a, b listedArtifact) int { return strings.Compare(a.URI, b.URI) })
	return slices.CompactFunc(items, func(a, b listedArtifact) bool { return a.URI == b.URI })
}

// All formats use the same rows. Names maps exact dimension identifiers to
// unambiguous CLI flag names; it does not change item identity.
func writeArtifactList(cmd *cobra.Command, items []listedArtifact, names map[string]string) (rerr error) {
	format, _ := cmd.Flags().GetString("format")
	if err := validateArtifactListFormat(format); err != nil {
		return err
	}
	items = uniqueArtifactList(items)
	if len(items) == 0 {
		return nil
	}
	// Every write to the command's stdout becomes a telemetry log record,
	// which the frontend stores, parses and replays one at a time. A table
	// writes each cell and each run of padding separately, so a long listing
	// would be tens of thousands of records: buffer it into a few.
	out := &lineChunks{w: cmd.OutOrStdout(), size: 32 << 10}
	defer func() {
		if err := out.Flush(); rerr == nil {
			rerr = err
		}
	}()
	if format == "link" {
		for _, item := range items {
			if _, err := fmt.Fprintln(out, item.URI); err != nil {
				return err
			}
		}
		return nil
	}
	if format == "cli" {
		return writeArtifactCLI(out, items, names, artifactListReplayArgs(cmd), artifactCLICommand(cmd))
	}
	canonicalRows := make([][]flattening.Coordinate, len(items))
	for i, item := range items {
		canonicalRows[i] = artifactTableCoordinates(item)
	}
	dimensions := artifact.VisibleDimensions(canonicalRows)
	slices.SortStableFunc(dimensions, compareArtifactDimensions)
	// Mixed types use separate tables, not sparse columns for every type.
	groups := map[string][]listedArtifact{}
	var order []string
	for _, item := range items {
		typ := ""
		for _, key := range item.DimensionKeys {
			if strings.HasPrefix(key.Dimension, "type:") {
				typ = key.Dimension
			}
		}
		if _, ok := groups[typ]; !ok {
			order = append(order, typ)
		}
		groups[typ] = append(groups[typ], item)
	}
	for i, typ := range order {
		if i > 0 {
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
		}
		if err := writeArtifactTable(out, groups[typ], names, dimensions); err != nil {
			return err
		}
	}
	return nil
}

// lineChunks passes writes on in pieces of whole lines, each of at least size
// bytes but the last. Cutting only after a newline keeps each piece valid
// UTF-8 on its own, as a log record's body must be.
type lineChunks struct {
	w    io.Writer
	size int
	buf  []byte
}

func (c *lineChunks) Write(p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	if len(c.buf) < c.size {
		return len(p), nil
	}
	end := bytes.LastIndexByte(c.buf, '\n') + 1
	if end == 0 {
		return len(p), nil
	}
	_, err := c.w.Write(c.buf[:end])
	c.buf = append(c.buf[:0], c.buf[end:]...)
	return len(p), err
}

func (c *lineChunks) Flush() error {
	if len(c.buf) == 0 {
		return nil
	}
	_, err := c.w.Write(c.buf)
	c.buf = c.buf[:0]
	return err
}

// artifactTableCoordinates keeps table identity separate from legacy selectors.
func artifactTableCoordinates(item listedArtifact) []flattening.Coordinate {
	var coordinates []flattening.Coordinate
	if item.Coordinates != nil {
		for _, key := range item.Coordinates {
			coordinates = append(coordinates, flattening.Coordinate{Dimension: key.Dimension, Key: key.Key})
		}
	} else {
		// Older engines supply only legacy coordinates. Keep their full field path.
		for _, key := range item.DimensionKeys {
			if !strings.HasPrefix(key.Dimension, "type:") {
				coordinates = append(coordinates, flattening.Coordinate{Dimension: key.Dimension, Key: key.Key})
			}
		}
		name := item.Name
		if name == "" {
			name = strings.ReplaceAll(item.fullPath(), "/", ".")
		}
		if name != "" {
			coordinates = append(coordinates, flattening.Coordinate{Dimension: flattening.NameDimension, Key: name})
		}
	}
	for _, id := range item.Presence {
		coordinates = append(coordinates, flattening.Coordinate{Dimension: id, Key: "*"})
	}
	return coordinates
}

func writeArtifactTable(out io.Writer, items []listedArtifact, names map[string]string, dimensions []string) error {
	described, absolute := false, false
	for _, item := range items {
		described = described || firstDescriptionLine(item.Description) != ""
		addr, err := dagaddress.Parse(item.URI)
		if err != nil {
			return err
		}
		absolute = absolute || addr.Absolute
	}
	header := []string{}
	if absolute {
		header = append(header, "WORKSPACE")
	}
	for _, dimension := range dimensions {
		name := names[dimension]
		if dimension == flattening.NameDimension {
			name = "name"
		}
		if name == "" {
			name = dimension
		}
		header = append(header, strings.ToUpper(name))
	}
	if described {
		header = append(header, "DESCRIPTION")
	}
	header = append(header, "LINK")
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, strings.Join(header, "\t")); err != nil {
		return err
	}
	for _, item := range items {
		row := []string{}
		if absolute {
			addr, err := dagaddress.Parse(item.URI)
			if err != nil {
				return err
			}
			workspace := ""
			if addr.Absolute {
				workspace = addr.Workspace + "@" + addr.Version
			}
			row = append(row, workspace)
		}
		for _, dimension := range dimensions {
			row = append(row, artifactDimensionCell(item, dimension))
		}
		if described {
			row = append(row, firstDescriptionLine(item.Description))
		}
		row = append(row, item.URI)
		for i, value := range row {
			row[i] = strings.NewReplacer("\t", `\t`, "\r", `\r`, "\n", `\n`).Replace(value)
		}
		if _, err := fmt.Fprintln(writer, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return writer.Flush()
}

// Collections precede module and name, in that order.
func compareArtifactDimensions(a, b string) int {
	rank := func(id string) int {
		switch id {
		case "module":
			return 1
		case flattening.NameDimension:
			return 2
		default:
			return 0
		}
	}
	if n := rank(a) - rank(b); n != 0 {
		return n
	}
	return strings.Compare(a, b)
}

func artifactDimensionCell(item listedArtifact, dimension string) string {
	var values []string
	for _, key := range artifactTableCoordinates(item) {
		if key.Dimension != dimension {
			continue
		}
		value := key.Key
		if value == "" || strings.HasPrefix(value, "\"") || strings.ContainsAny(value, "\t\r\n") {
			value = strconv.Quote(value)
		}
		if !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	return strings.Join(values, ", ")
}

// artifactCLICommand uses the command's selected type or collection subcommand.
// Execution commands omit --list so a copied command executes the selected row.
func artifactCLICommand(cmd *cobra.Command) string {
	path := strings.Fields(cmd.CommandPath())
	if len(path) == 0 || path[0] != "dagger" {
		path = append([]string{"dagger"}, path...)
	}
	if child := cmd.Annotations[artifactPreviewListCommand]; child != "" {
		path = append(path, child)
	}
	return strings.Join(path, " ")
}

func writeArtifactCLI(w io.Writer, items []listedArtifact, names map[string]string, options []string, command string) error {
	lines := make([]commandListItem, 0, len(items))
	for _, item := range items {
		args, err := artifactCLIArguments(item, names, options)
		if err != nil {
			return err
		}
		lines = append(lines, commandListItem{Name: strings.TrimSpace(command + " " + args), Comment: firstDescriptionLine(item.Description)})
	}
	return writeCommandList(w, lines)
}

func artifactCLIArguments(item listedArtifact, names map[string]string, options []string) (string, error) {
	addr, err := dagaddress.Parse(item.URI)
	if err != nil {
		return "", err
	}
	var args, replay []string
	workspace, selected := "", false
	if addr.Absolute {
		workspace, selected = addr.Workspace+"@"+addr.Version, true
	}
	for i := 0; i < len(options); i++ {
		if options[i] == "-W" && i+1 < len(options) {
			if !addr.Absolute {
				workspace, selected = options[i+1], true
			}
			i++
			continue
		}
		replay = append(replay, options[i])
	}
	if selected {
		quoted, err := quoteArtifactArgument(workspace)
		if err != nil {
			return "", err
		}
		args = append(args, "-W", quoted)
	}
	selector := &dagaddress.Address{}
	if item.CLIPath != "" {
		var err error
		selector, err = dagaddress.Parse(item.CLIPath)
		if err != nil {
			return "", err
		}
	}
	if address, err := dagaddress.Parse(item.URI); err == nil {
		for _, pair := range address.Query {
			if pair.Absent && !slices.Contains(selector.Query, pair) {
				selector.Query = append(selector.Query, pair)
			}
		}
	}
	if item.CLIPath != "" || len(selector.Query) > 0 {
		quoted, err := quoteArtifactArgument(selector.String())
		if err != nil {
			return "", err
		}
		args = append(args, quoted)
	}
	coordinates := item.DimensionKeys
	modern := item.Coordinates != nil
	if modern {
		coordinates = item.Coordinates
	}
	terminal := func(id string) bool {
		if modern {
			return id == artifact.NameDimension
		}
		return strings.HasPrefix(id, "type:")
	}
	for _, typeKeys := range []bool{false, true} {
		for _, key := range coordinates {
			if terminal(key.Dimension) != typeKeys {
				continue
			}
			if typeKeys && (item.OmitCLITypeKey || item.CollectionItem && item.OmitTypeKey) {
				continue
			}
			if key.Dimension == artifact.ModuleDimension && item.ModuleFlag != "" {
				quoted, err := quoteArtifactArgument("--" + item.ModuleFlag)
				if err != nil {
					return "", err
				}
				args = append(args, quoted)
				continue
			}
			name := names[key.Dimension]
			if modern && key.Dimension == artifact.NameDimension {
				name = "name"
			}
			if name == "" {
				name = key.Dimension
			}
			value := key.Key
			if display, ok := item.DisplayKeys[key.Dimension]; !modern && ok {
				value = display
			}
			quoted, err := quoteArtifactArgument(value)
			if err != nil {
				return "", err
			}
			args = append(args, "--"+name+"="+quoted)
		}
		if !typeKeys {
			for _, id := range item.Presence {
				name := item.PresenceNames[id]
				if name == "" {
					return "", fmt.Errorf("no collection flag for %q", id)
				}
				args = append(args, "--"+name)
			}
		}
	}

	for _, option := range replay {
		quoted, err := quoteArtifactArgument(option)
		if err != nil {
			return "", err
		}
		args = append(args, quoted)
	}
	return strings.Join(args, " "), nil
}

func quoteArtifactArgument(value string) (string, error) {
	// Quote also handles newlines, so every item stays on one physical line.
	quoted, err := syntax.Quote(value, syntax.LangBash)
	if err != nil {
		return "", err
	}
	// Protect interactive shell history expansion too.
	if strings.Contains(value, "!") && !strings.HasPrefix(quoted, "$'") {
		quoted = "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	return quoted, nil
}
