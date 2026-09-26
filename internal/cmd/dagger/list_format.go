package daggercmd

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/dagger/dagger/core/artifact"
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

// All formats use the same rows. Names maps exact dimension identifiers to
// unambiguous CLI flag names; it does not change item identity.
func writeArtifactList(cmd *cobra.Command, items []listedArtifact, names map[string]string) error {
	format, _ := cmd.Flags().GetString("format")
	if err := validateArtifactListFormat(format); err != nil {
		return err
	}
	slices.SortFunc(items, func(a, b listedArtifact) int { return strings.Compare(a.URI, b.URI) })
	items = slices.CompactFunc(items, func(a, b listedArtifact) bool { return a.URI == b.URI })
	if len(items) == 0 {
		return nil
	}
	if format == "link" {
		for _, item := range items {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), item.URI); err != nil {
				return err
			}
		}
		return nil
	}
	if format == "cli" {
		return writeArtifactCLI(cmd.OutOrStdout(), items, names, artifactListReplayArgs(cmd))
	}
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
			if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
				return err
			}
		}
		if err := writeArtifactTable(cmd.OutOrStdout(), groups[typ], names); err != nil {
			return err
		}
	}
	return nil
}

func writeArtifactTable(out io.Writer, items []listedArtifact, names map[string]string) error {
	var dimensions []string
	described, absolute := false, false
	for _, item := range items {
		for _, key := range item.DimensionKeys {
			if !slices.Contains(dimensions, key.Dimension) {
				dimensions = append(dimensions, key.Dimension)
			}
		}
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
		if name == "" {
			name = dimension
		}
		header = append(header, strings.ToUpper(name))
	}
	if described {
		header = append(header, "DESCRIPTION")
	}
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
		for i, value := range row {
			row[i] = strings.NewReplacer("\t", `\t`, "\r", `\r`, "\n", `\n`).Replace(value)
		}
		if _, err := fmt.Fprintln(writer, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func artifactDimensionCell(item listedArtifact, dimension string) string {
	var values []string
	for _, key := range item.DimensionKeys {
		if key.Dimension != dimension {
			continue
		}
		value := key.Key
		if display, ok := item.DisplayKeys[dimension]; ok {
			value = display
		}
		if value == "" || strings.HasPrefix(value, "\"") || strings.ContainsAny(value, "\t\r\n") {
			value = strconv.Quote(value)
		}
		values = append(values, value)
	}
	return strings.Join(values, ", ")
}

func writeArtifactCLI(w io.Writer, items []listedArtifact, names map[string]string, options []string) error {
	lines := make([]commandListItem, 0, len(items))
	for _, item := range items {
		args, err := artifactCLIArguments(item, names, options)
		if err != nil {
			return err
		}
		lines = append(lines, commandListItem{Name: args, Comment: firstDescriptionLine(item.Description)})
	}
	return writeCommandList(w, lines)
}

func artifactCLIArguments(item listedArtifact, names map[string]string, options []string) (string, error) {
	addr, err := dagaddress.Parse(item.URI)
	if err != nil {
		return "", err
	}
	var args []string
	if addr.Absolute {
		workspace, err := quoteArtifactArgument(addr.Workspace + "@" + addr.Version)
		if err != nil {
			return "", err
		}
		args = append(args, "-W", workspace)
	}
	for _, typeKeys := range []bool{false, true} {
		for _, key := range item.DimensionKeys {
			if strings.HasPrefix(key.Dimension, "type:") != typeKeys {
				continue
			}
			if typeKeys && item.OmitCLITypeKey {
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
			if name == "" {
				name = key.Dimension
			}
			value := key.Key
			if display, ok := item.DisplayKeys[key.Dimension]; ok {
				value = display
			}
			quoted, err := quoteArtifactArgument(value)
			if err != nil {
				return "", err
			}
			args = append(args, "--"+name+"="+quoted)
		}
	}

	for _, option := range options {
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
