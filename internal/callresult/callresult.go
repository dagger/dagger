// Package callresult prints the result of a function call the way `dagger
// call` does. The CLI uses it for the calls it runs, and the engine for a
// detached query, which runs on its own after its client has left.
package callresult

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
)

// The formats a result is printed in.
const (
	// FormatPlain prints scalars as they are, and lists one item per line.
	FormatPlain = "plain"
	// FormatJSON prints the result as indented JSON (`dagger call --json`).
	FormatJSON = "json"
	// FormatID prints objects as their type and digest.
	FormatID = "id"
)

// WriteSelected prints the value a call's query selects from its GraphQL
// response data, as the CLI prints a call's result.
func WriteSelected(w io.Writer, format, query string, data any) error {
	result, err := Selected(query, data)
	if err != nil {
		return err
	}
	switch format {
	case FormatJSON:
		return JSON(w, result)
	case FormatID:
		return ID(w, result)
	default:
		if result == nil {
			return nil
		}
		return Plain(w, result)
	}
}

// Selected returns the value a call's query selects from its response data.
// The query selects one field per function in the call's chain. As the CLI's
// query builder unpacks it, the value is at the end of that path, or at the
// first list on the way, which is kept whole with its items' fields.
func Selected(query string, data any) (any, error) {
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		return nil, err
	}
	if len(doc.Operations) != 1 {
		return nil, fmt.Errorf("query has %d operations", len(doc.Operations))
	}
	var path []string
	for set := doc.Operations[0].SelectionSet; len(set) > 0; {
		field, ok := set[0].(*ast.Field)
		if len(set) != 1 || !ok {
			return nil, fmt.Errorf("query does not select a single field")
		}
		path = append(path, field.Alias)
		set = field.SelectionSet
	}
	return selectPath(data, path), nil
}

func selectPath(v any, path []string) any {
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			break
		}
		v = m[key]
	}
	return v
}

// JSON prints a result as indented JSON.
func JSON(w io.Writer, result any) error {
	// disable HTML escaping to improve readability
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "    ")
	return encoder.Encode(result)
}

// Plain prints a result with no decoration.
func Plain(w io.Writer, r any) error {
	switch t := r.(type) {
	case []any:
		for _, v := range t {
			if err := Plain(w, v); err != nil {
				return err
			}
			fmt.Fprintln(w)
		}
		return nil
	case map[string]any:
		// NB: we're only interested in values because this is where we unwrap
		// things like {"container":{"from":{"withExec":{"stdout":"foo"}}}}.
		for _, v := range t {
			if err := Plain(w, v); err != nil {
				return err
			}
		}
		return nil
	case string:
		fmt.Fprint(w, t)
	default:
		fmt.Fprintf(w, "%+v", t)
	}
	return nil
}

// ID prints an object result, or a list of them, as type and digest.
func ID(w io.Writer, result any) error {
	switch v := result.(type) {
	case []any:
		for _, item := range v {
			fmt.Fprint(w, "- ")
			if err := ID(w, item); err != nil {
				return err
			}
		}
		return nil
	case nil:
		// A nullable object field that resolved to null. "null" is both valid
		// JSON and unambiguous in plain output.
		_, err := fmt.Fprintln(w, "null")
		return err
	case string:
		return EncodedID(w, v)
	case map[string]any:
		id, ok := v["id"]
		if !ok {
			return fmt.Errorf("printID: no ID found in object: %+v", v)
		}
		return ID(w, id)
	default:
		return fmt.Errorf("printID: unexpected type for object: %T", v)
	}
}

// EncodedID prints an encoded object ID as type and digest.
func EncodedID(w io.Writer, encodedID string) error {
	if encodedID == "" {
		// special case: return value was the root object (Query itself)
		fmt.Fprintln(w, "Query")
		return nil
	}
	var id call.ID
	if err := id.Decode(encodedID); err != nil {
		return fmt.Errorf("failed to decode ID: %w", err)
	}
	dig, err := idDigest(encodedID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s@%s\n", id.Type().ToAST().Name(), dig)
	return err
}

func idDigest(encodedID string) (digest.Digest, error) {
	var id call.ID
	if err := id.Decode(encodedID); err != nil {
		return "", fmt.Errorf("failed to decode ID: %w", err)
	}
	if id.IsHandle() {
		return hashutil.HashStrings(encodedID), nil
	}
	return id.Digest(), nil
}
