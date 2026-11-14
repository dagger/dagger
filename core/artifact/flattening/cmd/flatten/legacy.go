package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/dagger/dagger/core/artifact/flattening"
)

type legacyInputArtifact struct {
	flattening.Artifact
	Items [][]string `json:"items"`
}

func runLegacy(only string, input io.Reader, output io.Writer) error {
	var entries []legacyInputArtifact
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return fmt.Errorf("read schema: %w", err)
		}
		return fmt.Errorf("expected one JSON array")
	}
	schema := make([]flattening.Artifact, len(entries))
	for i, entry := range entries {
		schema[i] = entry.Artifact
	}
	p := flattening.New(schema)
	dimensions := p.Dimensions()
	names := p.Names()
	flagNames := flattening.DimensionFlags(dimensions, nil, nil)
	var rows [][]string
	for _, entry := range entries {
		path := strings.Join(entry.Path, "/")
		if only != "" && path != only {
			continue
		}
		for _, keys := range entry.Items {
			row, err := p.Flatten(entry.Artifact, keys...)
			if err != nil {
				return err
			}
			coordinates := make(map[string]string, len(row.Coordinates))
			for _, coordinate := range row.Coordinates {
				if _, exists := coordinates[coordinate.Dimension]; exists {
					return fmt.Errorf("artifact %s has repeated dimension %s", path, coordinate.Dimension)
				}
				coordinates[coordinate.Dimension] = coordinate.Key
			}
			values := []string{path}
			for _, dimension := range dimensions {
				value, exists := coordinates[dimension.Identifier]
				if !exists {
					values = append(values, "-")
				} else if value == "" || value == "-" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\t\r\n") {
					values = append(values, strconv.Quote(value))
				} else {
					values = append(values, value)
				}
			}
			rows = append(rows, values)
		}
	}
	w := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDISPLAY NAME\tKEY FLAG\tPRESENCE FLAG")
	for _, dimension := range dimensions {
		id := dimension.Identifier
		if flagNames[id].Presence == "" {
			fmt.Fprintf(w, "%s\t%s\t%s\n", id, names[id], flagNames[id].Key)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", id, names[id], flagNames[id].Key, flagNames[id].Presence)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w)
	header := []string{"PATH"}
	for _, dimension := range dimensions {
		header = append(header, dimension.Identifier)
	}
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}
