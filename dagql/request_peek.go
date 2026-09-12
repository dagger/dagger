package dagql

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

type peekGraphQLRequest struct {
	Query         string                     `json:"query"`
	OperationName string                     `json:"operationName"`
	Variables     map[string]json.RawMessage `json:"variables"`
}

// RootFieldPeek describes what a GraphQL-over-HTTP request selects at its root.
type RootFieldPeek struct {
	// Fields is the deduplicated list of top-level field names.
	Fields []string
	// NodeIDs holds the encoded ids passed to top-level `node(id:)` selections.
	NodeIDs []string
	// UnresolvedNodeIDs reports that a top-level `node` selection's id could
	// not be read (a missing variable, or a non-literal value), so NodeIDs does
	// not account for every `node` selection.
	UnresolvedNodeIDs bool
}

// PeekRootFields returns the top-level fields selected by a GraphQL-over-HTTP
// request while preserving the request body for the real server. The operation
// to inspect is chosen by the request's operation name (or the sole operation
// when the document has just one).
func PeekRootFields(r *http.Request) (bool, RootFieldPeek, error) {
	query, operationName, variables, ok, err := peekGraphQLRequestBody(r)
	if err != nil || !ok {
		return ok, RootFieldPeek{}, err
	}

	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		return false, RootFieldPeek{}, err
	}

	op, ok := peekOperation(doc, operationName)
	if !ok || op.Operation != ast.Query {
		return false, RootFieldPeek{}, nil
	}

	peek, ok := collectRootFields(op.SelectionSet, doc.Fragments, variables)
	if !ok {
		return false, RootFieldPeek{}, nil
	}
	return true, peek, nil
}

func peekGraphQLRequestBody(r *http.Request) (string, string, map[string]json.RawMessage, bool, error) {
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query().Get("query")
		if query == "" {
			return "", "", nil, false, nil
		}
		var variables map[string]json.RawMessage
		if raw := r.URL.Query().Get("variables"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &variables); err != nil {
				return "", "", nil, false, err
			}
		}
		return query, r.URL.Query().Get("operationName"), variables, true, nil
	case http.MethodPost:
		if r.Body == nil {
			return "", "", nil, false, nil
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return "", "", nil, false, err
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil && r.Header.Get("Content-Type") != "" {
			return "", "", nil, false, nil
		}
		if mediaType == "application/graphql" {
			query := string(body)
			if strings.TrimSpace(query) == "" {
				return "", "", nil, false, nil
			}
			return query, r.URL.Query().Get("operationName"), nil, true, nil
		}
		if mediaType != "" && mediaType != "application/json" {
			return "", "", nil, false, nil
		}

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 || trimmed[0] == '[' {
			return "", "", nil, false, nil
		}
		var payload peekGraphQLRequest
		if err := json.Unmarshal(trimmed, &payload); err != nil {
			return "", "", nil, false, err
		}
		if strings.TrimSpace(payload.Query) == "" {
			return "", "", nil, false, nil
		}
		return payload.Query, payload.OperationName, payload.Variables, true, nil
	default:
		return "", "", nil, false, nil
	}
}

func peekOperation(doc *ast.QueryDocument, operationName string) (*ast.OperationDefinition, bool) {
	if operationName != "" {
		for _, op := range doc.Operations {
			if op.Name == operationName {
				return op, true
			}
		}
		return nil, false
	}
	if len(doc.Operations) != 1 {
		return nil, false
	}
	return doc.Operations[0], true
}

func collectRootFields(selections ast.SelectionSet, fragments ast.FragmentDefinitionList, variables map[string]json.RawMessage) (RootFieldPeek, bool) {
	peek := RootFieldPeek{Fields: make([]string, 0, len(selections))}
	seenFields := map[string]struct{}{}
	if !appendRootFields(selections, fragments, variables, map[string]bool{}, seenFields, &peek) {
		return RootFieldPeek{}, false
	}
	return peek, true
}

func appendRootFields(selections ast.SelectionSet, fragments ast.FragmentDefinitionList, variables map[string]json.RawMessage, fragmentStack map[string]bool, seenFields map[string]struct{}, peek *RootFieldPeek) bool {
	for _, selection := range selections {
		switch sel := selection.(type) {
		case *ast.Field:
			// node ids are collected per selection, before the dedupe: aliased
			// `node` selections all contribute their own demand.
			if sel.Name == "node" {
				if id, ok := peekNodeID(sel, variables); ok {
					peek.NodeIDs = append(peek.NodeIDs, id)
				} else {
					peek.UnresolvedNodeIDs = true
				}
			}
			if _, ok := seenFields[sel.Name]; ok {
				continue
			}
			seenFields[sel.Name] = struct{}{}
			peek.Fields = append(peek.Fields, sel.Name)
		case *ast.InlineFragment:
			if !appendRootFields(sel.SelectionSet, fragments, variables, fragmentStack, seenFields, peek) {
				return false
			}
		case *ast.FragmentSpread:
			if fragmentStack[sel.Name] {
				return false
			}
			fragment := fragments.ForName(sel.Name)
			if fragment == nil {
				return false
			}
			fragmentStack[sel.Name] = true
			ok := appendRootFields(fragment.SelectionSet, fragments, variables, fragmentStack, seenFields, peek)
			delete(fragmentStack, sel.Name)
			if !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func peekNodeID(sel *ast.Field, variables map[string]json.RawMessage) (string, bool) {
	arg := sel.Arguments.ForName("id")
	if arg == nil || arg.Value == nil {
		return "", false
	}
	switch arg.Value.Kind {
	case ast.StringValue:
		return arg.Value.Raw, true
	case ast.Variable:
		raw, ok := variables[arg.Value.Raw]
		if !ok {
			return "", false
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", false
		}
		return id, true
	default:
		return "", false
	}
}
