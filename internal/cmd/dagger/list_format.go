package daggercmd

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

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
	var dimensions []string
	var rows [][]string
	var lines []commandListItem
	described, needsLink := false, false
	for _, item := range items {
		for _, key := range item.DimensionKeys {
			if !slices.Contains(dimensions, key.Dimension) {
				dimensions = append(dimensions, key.Dimension)
			}
		}
		described = described || firstDescriptionLine(item.Description) != ""
	}
	seenKeys := map[string]bool{}
	for _, item := range items {
		addr, err := dagaddress.Parse(item.URI)
		if err != nil {
			return err
		}
		addr.Query = nil
		values := make([]string, len(dimensions))
		var flags []string
		for _, key := range item.DimensionKeys {
			values[slices.Index(dimensions, key.Dimension)] = key.Key
			name := names[key.Dimension]
			if name == "" {
				name = key.Dimension
			}
			value, err := quoteArtifactArgument(key.Key)
			if err != nil {
				return err
			}
			flags = append(flags, "--"+name+"="+value)
		}
		// Length prefixes distinguish empty keys, missing keys, and separator
		// characters. Different artifacts with the same tuple need a link.
		var identity strings.Builder
		for _, dimension := range dimensions {
			found := false
			for _, key := range item.DimensionKeys {
				if key.Dimension == dimension {
					fmt.Fprintf(&identity, "%d:%s;", len(key.Key), key.Key)
					found = true
					break
				}
			}
			if !found {
				identity.WriteString("-;")
			}
		}
		needsLink = needsLink || len(item.DimensionKeys) == 0 || seenKeys[identity.String()]
		seenKeys[identity.String()] = true
		if described {
			values = append(values, firstDescriptionLine(item.Description))
		}
		rows = append(rows, append(values, addr.String()))
		switch format {
		case "link":
			lines = append(lines, commandListItem{Name: item.URI})
		case "cli":
			// Retain the typed path. Keys alone can select other artifact paths,
			// including paths outside this command's type or address filter.
			link, err := quoteArtifactArgument(addr.String())
			if err != nil {
				return err
			}
			flags = append(flags, link)
			lines = append(lines, commandListItem{Name: strings.Join(flags, " "), Comment: firstDescriptionLine(item.Description)})
		}
	}
	if format != "table" {
		return writeCommandList(cmd.OutOrStdout(), lines)
	}
	var header []string
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
	if needsLink {
		header = append(header, "LINK")
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, strings.Join(header, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		if !needsLink {
			row = row[:len(row)-1]
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
