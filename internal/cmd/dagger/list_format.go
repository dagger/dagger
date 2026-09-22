package daggercmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
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
	if format == "link" {
		for _, item := range items {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), item.URI); err != nil {
				return err
			}
		}
		return nil
	}
	if format == "cli" {
		var lines []commandListItem
		for _, item := range items {
			addr, err := dagaddress.Parse(item.URI)
			if err != nil {
				return err
			}
			addr.Query = nil
			var flags []string
			for _, key := range item.DimensionKeys {
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
			// Keys alone can select paths outside this type or address filter.
			link, err := quoteArtifactArgument(addr.String())
			if err != nil {
				return err
			}
			flags = append(flags, link)
			lines = append(lines, commandListItem{Name: strings.Join(flags, " "), Comment: firstDescriptionLine(item.Description)})
		}
		return writeCommandList(cmd.OutOrStdout(), lines)
	}
	var dimensions []string
	var rows [][]string
	described, needsVariant := false, false
	var paths []string
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
		values := make([]string, len(dimensions))
		for _, key := range item.DimensionKeys {
			value := key.Key
			// An empty key differs from an absent dimension. Quote control
			// characters and literal quoted strings so they remain distinct.
			if value == "" || strings.HasPrefix(value, "\"") || strings.ContainsAny(value, "\t\r\n") {
				value = strconv.Quote(value)
			}
			values[slices.Index(dimensions, key.Dimension)] = value
		}
		// Compare the displayed tuple, including absent and empty keys.
		identity, _ := json.Marshal(values)
		needsVariant = needsVariant || len(item.DimensionKeys) == 0 || seenKeys[string(identity)]
		seenKeys[string(identity)] = true
		if described {
			values = append(values, firstDescriptionLine(item.Description))
		}
		rows = append(rows, append(values, addr.Path))
		paths = append(paths, addr.Path)
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
	if needsVariant {
		header = append(header, "VARIANT")
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, strings.Join(header, "\t")); err != nil {
		return err
	}
	var variants map[string]string
	if needsVariant {
		variants = artifactVariantLabels(paths)
	}
	for _, row := range rows {
		if needsVariant {
			row[len(row)-1] = variants[row[len(row)-1]]
		} else {
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

// Count each suffix once per distinct path. Labels depend only on paths, not
// their order or how many dimension rows use each path.
func artifactVariantLabels(paths []string) map[string]string {
	parts := map[string][]string{}
	counts := map[string]int{}
	for _, path := range paths {
		if _, exists := parts[path]; exists {
			continue
		}
		parts[path] = strings.Split(path, "/")
		for i := range parts[path] {
			counts[strings.Join(parts[path][i:], "/")]++
		}
	}
	labels := map[string]string{}
	for path, segments := range parts {
		label := path
		for i := len(segments) - 1; i >= 0; i-- {
			suffix := strings.Join(segments[i:], "/")
			if counts[suffix] == 1 {
				label = suffix
				break
			}
		}
		labels[path] = label
	}
	return labels
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
