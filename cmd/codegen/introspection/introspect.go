package introspection

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"

	"github.com/dagger/dagger/engine/naming"
)

// Introspect gets the Dagger Schema, with the words of its names when the
// engine has them (see FetchIdentifiers).
func Introspect(ctx context.Context, dag *dagger.Client) (*Schema, string, error) {
	var introspectionResp Response
	err := dag.Do(ctx, &dagger.Request{
		Query:  Query,
		OpName: "IntrospectionQuery",
	}, &dagger.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, "", fmt.Errorf("introspection query: %w", err)
	}
	if err := FetchIdentifiers(ctx, dag, introspectionResp.Schema); err != nil {
		return nil, "", err
	}

	return introspectionResp.Schema, introspectionResp.SchemaVersion, nil
}

// identifierBatchSize bounds the number of names parsed per request.
const identifierBatchSize = 500

// FetchIdentifiers sets schema.Identifiers from the engine's Query.identifier
// API, giving a schema from the GraphQL introspection query the same words
// the engine writes to the schema JSON's "__identifiers". The engine parses
// the names with the dictionary for the client's version. An engine whose
// schema has no Query.identifier (views before v1.0.0) leaves Identifiers
// nil, so codegen keeps its own conversion.
func FetchIdentifiers(ctx context.Context, dag *dagger.Client, schema *Schema) error {
	if schema == nil || schema.Query() == nil || !hasField(schema.Query(), "identifier") {
		return nil
	}

	names := schema.identifierNames()

	ids := Identifiers{}
	for start := 0; start < len(names); start += identifierBatchSize {
		batch := names[start:min(start+identifierBatchSize, len(names))]
		var query strings.Builder
		query.WriteString("query Identifiers {")
		for i, name := range batch {
			quoted, err := json.Marshal(name)
			if err != nil {
				return err
			}
			fmt.Fprintf(&query, " i%d: identifier(name: %s) { words { kind text suffix term { spelling capitalized } } }", i, quoted)
		}
		query.WriteString(" }")

		var data map[string]struct {
			Words []struct {
				Kind   string
				Text   string
				Suffix string
				Term   *struct {
					Spelling    string
					Capitalized string
				}
			}
		}
		if err := dag.Do(ctx, &dagger.Request{
			Query:  query.String(),
			OpName: "Identifiers",
		}, &dagger.Response{
			Data: &data,
		}); err != nil {
			return fmt.Errorf("identifier query: %w", err)
		}

		for i, name := range batch {
			result, ok := data[fmt.Sprintf("i%d", i)]
			if !ok {
				return fmt.Errorf("identifier query: no result for %q", name)
			}
			id := naming.Identifier{Name: name}
			for _, w := range result.Words {
				word := naming.Word{Text: w.Text, Suffix: w.Suffix}
				switch w.Kind {
				case naming.KindWord.String():
					word.Kind = naming.KindWord
				case naming.KindAcronym.String():
					word.Kind = naming.KindAcronym
				case naming.KindTerm.String():
					word.Kind = naming.KindTerm
				default:
					return fmt.Errorf("identifier query: %q has a word of unknown kind %q", name, w.Kind)
				}
				if w.Term != nil {
					word.Term = &naming.Term{Spelling: w.Term.Spelling, Capitalized: w.Term.Capitalized}
				}
				id.Words = append(id.Words, word)
			}
			ids[name] = NewIdentifierWords(id)
		}
	}
	schema.Identifiers = ids
	return nil
}

func hasField(t *Type, name string) bool {
	for _, f := range t.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
