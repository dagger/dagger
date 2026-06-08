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

// PeekWorkspaceGeneratorsInclude reports whether a GraphQL-over-HTTP request is a
// workspace-generators query of the shape
//
//	{ currentWorkspace { generators(include: [...]) ... } }
//
// and, if so, returns the literal include patterns while preserving the request
// body for the real server. Workspace-rooted queries normally require the full
// workspace schema (every module loaded); recognizing this shape lets the engine
// narrow module loading to the generators actually requested, e.g.
// `dagger generate <module>`. It is deliberately conservative: anything other
// than a single currentWorkspace root field selecting only generators with a
// literal include list returns false, so loading falls back to all modules.
func PeekWorkspaceGeneratorsInclude(r *http.Request) (bool, []string, error) {
	query, operationName, ok, err := peekGraphQLRequestBody(r)
	if err != nil || !ok {
		return false, nil, err
	}

	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		return false, nil, err
	}

	op, ok := peekOperation(doc, operationName)
	if !ok || op.Operation != ast.Query {
		return false, nil, nil
	}

	root := singleConcreteField(op.SelectionSet)
	if root == nil || root.Name != "currentWorkspace" {
		return false, nil, nil
	}

	generators := singleConcreteField(root.SelectionSet)
	if generators == nil || generators.Name != "generators" {
		return false, nil, nil
	}

	include, ok := stringListArgument(generators.Arguments, "include")
	if !ok {
		return false, nil, nil
	}
	return true, include, nil
}

// singleConcreteField returns the sole non-introspection field in a selection
// set, or nil if there is not exactly one such field or the set contains
// fragments. Fragments are treated as "don't narrow" so callers stay on the
// safe (load-everything) path rather than reasoning about fragment expansion.
func singleConcreteField(set ast.SelectionSet) *ast.Field {
	var found *ast.Field
	for _, selection := range set {
		field, ok := selection.(*ast.Field)
		if !ok {
			return nil
		}
		if field.Name == "__typename" {
			continue
		}
		if found != nil {
			return nil
		}
		found = field
	}
	return found
}

// stringListArgument returns the values of a named argument when it is a literal
// list of strings. Missing, non-list, variable, or non-string arguments return
// false so callers do not narrow on something they can't resolve statically.
func stringListArgument(args ast.ArgumentList, name string) ([]string, bool) {
	arg := args.ForName(name)
	if arg == nil || arg.Value == nil || arg.Value.Kind != ast.ListValue {
		return nil, false
	}
	values := make([]string, 0, len(arg.Value.Children))
	for _, child := range arg.Value.Children {
		if child.Value == nil || child.Value.Kind != ast.StringValue {
			return nil, false
		}
		values = append(values, child.Value.Raw)
	}
	if len(values) == 0 {
		return nil, false
	}
	return values, true
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
