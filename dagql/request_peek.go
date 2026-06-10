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
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
}

// PeekRootFields returns the top-level field names selected by a GraphQL-over-HTTP
// request while preserving the request body for the real server.
func PeekRootFields(r *http.Request) (bool, []string, error) {
	query, operationName, _, ok, err := peekGraphQLRequestBody(r)
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

// workspaceIncludeSelectorFields are the currentWorkspace fields that enumerate
// module-provided items filtered by an `include` list of `module:item` patterns.
// They back `dagger generate`, `dagger check`, and `dagger up` respectively.
// Each normally needs the full workspace schema (every module loaded), but an
// `include` argument names exactly which modules are required, so module loading
// can be narrowed to them.
var workspaceIncludeSelectorFields = map[string]struct{}{
	"generators": {},
	"checks":     {},
	"services":   {},
}

// PeekWorkspaceSelectorInclude reports whether a GraphQL-over-HTTP request is a
// workspace item-selection query of the shape
//
//	{ currentWorkspace { <generators|checks|services>(include: [...]) ... } }
//
// and, if so, returns the literal include patterns while preserving the request
// body for the real server. Workspace-rooted queries normally require the full
// workspace schema (every module loaded); recognizing this shape lets the engine
// narrow module loading to the items actually requested, e.g.
// `dagger generate <module>`, `dagger check <module>`, or `dagger up <module>`.
// It is deliberately conservative: anything other than a single currentWorkspace
// root field selecting only one of those fields with a literal include list
// returns false, so loading falls back to all modules.
func PeekWorkspaceSelectorInclude(r *http.Request) (bool, []string, error) {
	query, operationName, vars, ok, err := peekGraphQLRequestBody(r)
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

	selector := singleConcreteField(root.SelectionSet)
	if selector == nil {
		return false, nil, nil
	}
	if _, ok := workspaceIncludeSelectorFields[selector.Name]; !ok {
		return false, nil, nil
	}

	include, ok := stringListArgument(selector.Arguments, "include", vars)
	if !ok {
		return false, nil, nil
	}
	return true, include, nil
}

// PeekWorkspaceTypeDefsInclude reports whether a GraphQL-over-HTTP request is a
// typedefs introspection of the shape
//
//	{ currentTypeDefs(… include: [...]) … }
//
// and, if so, returns the literal include patterns while preserving the request
// body for the real server. `dagger call` and `dagger functions` build their
// command tree from currentTypeDefs, which otherwise loads every workspace
// module; recognizing this shape lets the engine narrow module loading to the
// targeted module so an unrelated broken/stale module cannot block a call. The
// include argument may be a literal list or a query variable. It is deliberately
// conservative: anything other than a single currentTypeDefs root field with a
// resolvable string-list include returns false, so loading falls back to all
// modules.
func PeekWorkspaceTypeDefsInclude(r *http.Request) (bool, []string, error) {
	query, operationName, vars, ok, err := peekGraphQLRequestBody(r)
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
	if root == nil || root.Name != "currentTypeDefs" {
		return false, nil, nil
	}

	include, ok := stringListArgument(root.Arguments, "include", vars)
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

// stringListArgument returns the values of a named argument when it resolves to
// a non-empty list of strings, either as a literal list or a query variable
// resolved against vars. Missing, non-list, unresolved-variable, or non-string
// arguments return false so callers do not narrow on something they can't
// resolve statically.
func stringListArgument(args ast.ArgumentList, name string, vars map[string]any) ([]string, bool) {
	arg := args.ForName(name)
	if arg == nil || arg.Value == nil {
		return nil, false
	}
	switch arg.Value.Kind {
	case ast.ListValue:
		return stringListFromLiteral(arg.Value)
	case ast.Variable:
		raw, ok := vars[arg.Value.Raw]
		if !ok {
			return nil, false
		}
		return stringListFromAny(raw)
	default:
		return nil, false
	}
}

func stringListFromLiteral(value *ast.Value) ([]string, bool) {
	values := make([]string, 0, len(value.Children))
	for _, child := range value.Children {
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

func stringListFromAny(raw any) ([]string, bool) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	values := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		values = append(values, s)
	}
	return values, true
}

func peekGraphQLRequestBody(r *http.Request) (string, string, map[string]any, bool, error) {
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query().Get("query")
		if query == "" {
			return "", "", nil, false, nil
		}
		return query, r.URL.Query().Get("operationName"), parseVariablesParam(r.URL.Query().Get("variables")), true, nil
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

func parseVariablesParam(s string) map[string]any {
	if s == "" {
		return nil
	}
	var vars map[string]any
	if err := json.Unmarshal([]byte(s), &vars); err != nil {
		return nil
	}
	return vars
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
