package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/printer"
)

// TODO: this code is currently unused. We have started to question whether the possible benefits of laziness
// outweigh the costs in terms of DX cognitive complexity and have disabled laziness for now. It can be
// re-enabled if desired by flipping this const. Or this can all be removed if we are happy with unlaziness.
const enableLaziness = false

func shouldEval(ctx context.Context) bool {
	if enableLaziness {
		val, ok := ctx.Value(evalKey{}).(bool)
		return ok && val
	}
	return true
}

type evalKey struct{}

func withEval(ctx context.Context) context.Context {
	return context.WithValue(ctx, evalKey{}, true)
}

func lazyResolve(p graphql.ResolveParams) (interface{}, error) {
	var parentFieldNames []string
	for _, parent := range p.Info.Path.AsArray() {
		parentFieldNames = append(parentFieldNames, parent.(string))
	}
	lazyResult, err := getLazyResult(p,
		p.Info.ReturnType,
		parentFieldNames,
		p.Info.FieldASTs[0].SelectionSet,
	)
	if err != nil {
		return nil, err
	}
	return lazyResult, nil
}

func getLazyResult(p graphql.ResolveParams, output graphql.Output, parentFieldNames []string, selectionSet *ast.SelectionSet) (interface{}, error) {
	switch outputType := graphql.GetNullable(output).(type) {
	case *graphql.Scalar:
		selectedQuery, err := queryWithSelections(p.Info.Operation.(*ast.OperationDefinition), parentFieldNames)
		if err != nil {
			return nil, err
		}
		switch outputType.Name() {
		case "FS":
			bytes, err := FS{
				GraphQLRequest: GraphQLRequest{
					Query:         printer.Print(selectedQuery).(string),
					Variables:     p.Info.VariableValues,
					OperationName: getOperationName(p),
				},
			}.MarshalText()
			if err != nil {
				return nil, err
			}
			return string(bytes), nil
		case "DaggerString":
			return DaggerString{
				GraphQLRequest: GraphQLRequest{
					Query:         printer.Print(selectedQuery).(string),
					Variables:     p.Info.VariableValues,
					OperationName: getOperationName(p),
				},
			}.MarshalAny()
		default:
			return nil, fmt.Errorf("FIXME: currently unsupported scalar output type %s", outputType.Name())
		}
		// TODO: case *graphql.List: (may need to model lazy list using pagination)
	case *graphql.Object:
		result := make(map[string]interface{})
		for fieldName, field := range outputType.Fields() {
			// Check if this field is actually being selected, skip if not
			var selection *ast.Field
			for _, s := range selectionSet.Selections {
				s := s.(*ast.Field)
				if s.Name.Value == fieldName {
					selection = s
					break
				}
			}
			if selection == nil {
				continue
			}
			// Recurse to the selected field
			fieldNames := make([]string, len(parentFieldNames))
			copy(fieldNames, parentFieldNames)
			fieldNames = append(fieldNames, fieldName)
			subResult, err := getLazyResult(p, field.Type, fieldNames, selection.SelectionSet)
			if err != nil {
				return nil, err
			}
			result[fieldName] = subResult
		}
		return result, nil
	default:
		return nil, fmt.Errorf("FIXME: currently unsupported output type %T", output)
	}
}

func queryWithSelections(query *ast.OperationDefinition, fieldNames []string) (*ast.OperationDefinition, error) {
	newQuery := *query
	var err error
	newQuery.SelectionSet, err = filterSelectionSets(query.SelectionSet, fieldNames)
	if err != nil {
		return nil, err
	}
	return &newQuery, nil
}

func filterSelectionSets(selectionSet *ast.SelectionSet, fieldNames []string) (*ast.SelectionSet, error) {
	selectionSet, err := copySelectionSet(selectionSet)
	if err != nil {
		return nil, err
	}
	curSelectionSet := selectionSet
	for _, fieldName := range fieldNames {
		newSelectionSet, err := filterSelectionSet(curSelectionSet, fieldName)
		if err != nil {
			return nil, err
		}
		curSelectionSet.Selections = newSelectionSet.Selections
		curSelectionSet = newSelectionSet.Selections[0].(*ast.Field).SelectionSet
	}
	return selectionSet, nil
}

// return the selection set where the provided field is the only selection
func filterSelectionSet(selectionSet *ast.SelectionSet, fieldName string) (*ast.SelectionSet, error) {
	matchIndex := -1
	for i, selection := range selectionSet.Selections {
		selection := selection.(*ast.Field)
		if selection.Name.Value == fieldName {
			matchIndex = i
			break
		}
	}
	if matchIndex == -1 {
		return nil, fmt.Errorf("could not find %s in selectionSet %s", fieldName, printer.Print(selectionSet).(string))
	}
	selectionSet.Selections = []ast.Selection{selectionSet.Selections[matchIndex]}
	return selectionSet, nil
}

func copySelectionSet(selectionSet *ast.SelectionSet) (*ast.SelectionSet, error) {
	if selectionSet == nil {
		return nil, nil
	}
	var selections []ast.Selection
	for _, selection := range selectionSet.Selections {
		field, ok := selection.(*ast.Field)
		if !ok {
			return nil, fmt.Errorf("unsupported selection type %T", selection)
		}
		newField, err := copyField(field)
		if err != nil {
			return nil, err
		}
		selections = append(selections, newField)
	}
	return &ast.SelectionSet{Kind: selectionSet.Kind, Loc: selectionSet.Loc, Selections: selections}, nil
}

func copyField(field *ast.Field) (*ast.Field, error) {
	newField := *field
	var err error
	newField.SelectionSet, err = copySelectionSet(field.SelectionSet)
	if err != nil {
		return nil, err
	}
	return &newField, nil
}

// TODO: dedupe all the methods with equivalent in FS
type DaggerString struct {
	Value *string
	GraphQLRequest
}

func (s DaggerString) MarshalJSON() ([]byte, error) {
	a, err := s.MarshalAny()
	if err != nil {
		return nil, err
	}
	return json.Marshal(a)
}

func (s DaggerString) MarshalAny() (any, error) {
	if s.Value != nil {
		return s.Value, nil
	}
	type marshal DaggerString
	bytes, err := json.Marshal(marshal(s))
	if err != nil {
		return nil, err
	}
	return []any{
		base64.StdEncoding.EncodeToString(bytes),
	}, nil
}

func (s *DaggerString) UnmarshalJSON(data []byte) error {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	return s.UnmarshalAny(raw)
}

func (s *DaggerString) UnmarshalAny(data any) error {
	switch data := data.(type) {
	case string:
		s.Value = &data
	case []interface{}:
		if len(data) != 1 {
			return fmt.Errorf("invalid dagger string: %v", data)
		}
		raw, ok := data[0].(string)
		if !ok {
			return fmt.Errorf("invalid dagger string: %v", data)
		}
		bytes, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return err
		}
		type marshal DaggerString
		var result marshal
		if err := json.Unmarshal(bytes, &result); err != nil {
			return err
		}
		*s = DaggerString(result)
	default:
		return fmt.Errorf("invalid dagger string: %T(%+v)", data, data)
	}
	return nil
}

func (s DaggerString) Evaluate(ctx context.Context) (DaggerString, error) {
	for s.Value == nil {
		if s.Query == "" {
			return DaggerString{}, fmt.Errorf("invalid dagger string: missing query")
		}
		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  s.Query,
			Context:        withEval(ctx),
			VariableValues: s.Variables,
			OperationName:  s.OperationName,
		})
		if result.HasErrors() {
			return DaggerString{}, fmt.Errorf("dagger string eval errors: %+v", result.Errors)
		}

		// Extract the queried field out of the result
		resultMap := result.Data.(map[string]interface{})
		req, err := parser.Parse(parser.ParseParams{Source: s.Query})
		if err != nil {
			return DaggerString{}, err
		}
		field := req.Definitions[0].(*ast.OperationDefinition).SelectionSet.Selections[0].(*ast.Field)
		for field.SelectionSet != nil {
			resultMap = resultMap[field.Name.Value].(map[string]interface{})
			field = field.SelectionSet.Selections[0].(*ast.Field)
		}
		if err := s.UnmarshalAny(resultMap[field.Name.Value]); err != nil {
			return DaggerString{}, err
		}
	}
	return s, nil
}

// TODO: Evaluate needs to know which schema any query should be run against, put that inside FS (in a deterministic way to retain caching)
func (fs FS) Evaluate(ctx context.Context) (FS, error) {
	for fs.PB == nil {
		// TODO: guard against accidental infinite loop
		// this loop is where the "stack" is unwound, should later add debug info that traces each query leading to the final result
		if fs.Query == "" {
			return FS{}, fmt.Errorf("invalid fs: missing query")
		}
		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  fs.Query,
			Context:        withEval(ctx),
			VariableValues: fs.Variables,
			OperationName:  fs.OperationName,
		})
		if result.HasErrors() {
			return FS{}, fmt.Errorf("fs eval errors: %+v", result.Errors)
		}

		// Extract the queried field out of the result
		resultMap := result.Data.(map[string]interface{})
		req, err := parser.Parse(parser.ParseParams{Source: fs.Query})
		if err != nil {
			return FS{}, err
		}
		field := req.Definitions[0].(*ast.OperationDefinition).SelectionSet.Selections[0].(*ast.Field)
		for field.SelectionSet != nil {
			resultMap = resultMap[field.Name.Value].(map[string]interface{})
			field = field.SelectionSet.Selections[0].(*ast.Field)
		}
		rawFS, ok := resultMap[field.Name.Value].(string)
		if !ok {
			return FS{}, fmt.Errorf("invalid fs type %T", resultMap[field.Name.Value])
		}
		if err := fs.UnmarshalText([]byte(rawFS)); err != nil {
			return FS{}, err
		}
	}
	return fs, nil
}
