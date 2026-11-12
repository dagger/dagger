package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/dagger/dagger/core/artifact/flattening"
)

type inputField struct {
	flattening.Field
	Items []itemKeys `json:"items"`
}

// itemKeys rejects JSON null keys, which encoding/json otherwise reads as "".
type itemKeys []string

func (keys *itemKeys) UnmarshalJSON(data []byte) error {
	var values []*string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if values == nil {
		return fmt.Errorf("expected an array of collection keys")
	}
	*keys = make(itemKeys, len(values))
	for i, value := range values {
		if value == nil {
			return fmt.Errorf("collection key %d must be a string", i)
		}
		(*keys)[i] = *value
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet("flatten", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	only := flags.String("only", "", "Show rows with this exact name coordinate")
	legacy := flags.Bool("legacy", false, "Use legacy type-path input and output")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *legacy {
		legacyArgs := []string{}
		if *only != "" {
			legacyArgs = append(legacyArgs, "-only", *only)
		}
		return runLegacy(legacyArgs, input, output)
	}
	var entries []inputField
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
	if entries == nil {
		return fmt.Errorf("expected one JSON array")
	}
	fields := make([]flattening.Field, len(entries))
	for i, entry := range entries {
		fields[i] = entry.Field
	}
	schema, err := flattening.NewSchema(fields)
	if err != nil {
		return err
	}
	dimensions := schema.Dimensions()
	var rows [][]string
	seen := map[string]bool{}
	for i, entry := range entries {
		for j, keys := range entry.Items {
			if keys == nil {
				return fmt.Errorf("field %d item %d: expected an array of collection keys", i, j)
			}
			coordinates, err := schema.Coordinates(entry.Field, keys...)
			if err != nil {
				return fmt.Errorf("field %d item %d: %w", i, j, err)
			}
			cells := make(map[string]string, len(coordinates))
			for _, coordinate := range coordinates {
				if _, exists := cells[coordinate.Dimension]; exists {
					return fmt.Errorf("field %d item %d: repeated dimension %s", i, j, coordinate.Dimension)
				}
				cells[coordinate.Dimension] = coordinate.Key
			}
			values := []string{visible(entry.Type)}
			identity := []any{entry.Type}
			for _, dimension := range dimensions {
				value, exists := cells[dimension.Identifier]
				identity = append(identity, exists, value)
				if exists {
					values = append(values, visible(value))
				} else {
					values = append(values, "-")
				}
			}
			encoded, err := json.Marshal(identity)
			if err != nil {
				return err
			}
			if seen[string(encoded)] {
				return fmt.Errorf("field %d item %d: duplicate typed coordinates", i, j)
			}
			seen[string(encoded)] = true
			if *only == "" || cells[flattening.NameDimension] == *only {
				rows = append(rows, values)
			}
		}
	}
	w := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME")
	for _, dimension := range dimensions {
		fmt.Fprintf(w, "%s\t%s\n", visible(dimension.Identifier), visible(dimension.Name))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w)
	header := []string{"TYPE"}
	for _, dimension := range dimensions {
		header = append(header, visible(dimension.Name))
	}
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}

// visible distinguishes empty and control keys from absent coordinates.
func visible(value string) string {
	if value == "" || value == "-" || strings.TrimSpace(value) != value || strings.ContainsFunc(value, unicode.IsControl) {
		return strconv.Quote(value)
	}
	return value
}
