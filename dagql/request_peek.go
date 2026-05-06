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
	Query         string `json:"query"`
	OperationName string `json:"operationName"`
}

// PeekRootFields returns the top-level field names selected by a GraphQL-over-HTTP
// request while preserving the request body for the real server.
func PeekRootFields(r *http.Request) (bool, []string, error) {
	query, operationName, ok, err := peekGraphQLRequestBody(r)
	if err != nil || !ok {
		return ok, nil, err
	}

	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		return false, nil, err
	}

	op, ok := peekOperation(doc, operationName)
	if !ok || op.Operation != ast.Query {
		return false, nil, nil
	}

	fields, ok := collectRootFields(op.SelectionSet, doc.Fragments)
	if !ok {
		return false, nil, nil
	}
	return true, fields, nil
}

func peekGraphQLRequestBody(r *http.Request) (string, string, bool, error) {
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query().Get("query")
		if query == "" {
			return "", "", false, nil
		}
		return query, r.URL.Query().Get("operationName"), true, nil
	case http.MethodPost:
		if r.Body == nil {
			return "", "", false, nil
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return "", "", false, err
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil && r.Header.Get("Content-Type") != "" {
			return "", "", false, nil
		}
		if mediaType == "application/graphql" {
			query := string(body)
			if strings.TrimSpace(query) == "" {
				return "", "", false, nil
			}
			return query, r.URL.Query().Get("operationName"), true, nil
		}
		if mediaType != "" && mediaType != "application/json" {
			return "", "", false, nil
		}

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 || trimmed[0] == '[' {
			return "", "", false, nil
		}
		var payload peekGraphQLRequest
		if err := json.Unmarshal(trimmed, &payload); err != nil {
			return "", "", false, err
		}
		if strings.TrimSpace(payload.Query) == "" {
			return "", "", false, nil
		}
		return payload.Query, payload.OperationName, true, nil
	default:
		return "", "", false, nil
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

func collectRootFields(selections ast.SelectionSet, fragments ast.FragmentDefinitionList) ([]string, bool) {
	fields := make([]string, 0, len(selections))
	seenFields := map[string]struct{}{}
	if !appendRootFields(selections, fragments, map[string]bool{}, seenFields, &fields) {
		return nil, false
	}
	return fields, true
}

func appendRootFields(selections ast.SelectionSet, fragments ast.FragmentDefinitionList, fragmentStack map[string]bool, seenFields map[string]struct{}, fields *[]string) bool {
	for _, selection := range selections {
		switch sel := selection.(type) {
		case *ast.Field:
			if _, ok := seenFields[sel.Name]; ok {
				continue
			}
			seenFields[sel.Name] = struct{}{}
			*fields = append(*fields, sel.Name)
		case *ast.InlineFragment:
			if !appendRootFields(sel.SelectionSet, fragments, fragmentStack, seenFields, fields) {
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
			ok := appendRootFields(fragment.SelectionSet, fragments, fragmentStack, seenFields, fields)
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
