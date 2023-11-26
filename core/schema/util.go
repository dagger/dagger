package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/language/ast"
)

// idResolver is used to generate a scalar resolver for a stringable type.
func idResolver[T core.Object[T]]() ScalarResolver {
	return ScalarResolver{
		Serialize: func(value any) (any, error) {
			switch v := value.(type) {
			case string, T:
				return v, nil
			case *resourceid.ID[T]:
				return v.String(), nil
			default:
				var t T
				panic(fmt.Sprintf("want string or *resourceid.ID[%T], have %T: %+v", t, v, v))
			}
		},
		ParseValue: func(value any) (any, error) {
			switch v := value.(type) {
			case string:
				rid, err := resourceid.DecodeID[T](v)
				if err != nil {
					return nil, fmt.Errorf("failed to parse resource ID %q: %w", v, err)
				}
				return rid, nil
			default:
				return nil, fmt.Errorf("want string, have %T: %+v", v, v)
			}
		},
		ParseLiteral: func(valueAST ast.Value) (any, error) {
			switch v := valueAST.(type) {
			case *ast.StringValue:
				rid, err := resourceid.DecodeID[T](v.Value)
				if err != nil {
					return nil, fmt.Errorf("failed to parse resource ID %q: %w", v.Value, err)
				}
				return rid, nil
			default:
				return nil, fmt.Errorf("want *ast.StringValue, have %T: %+v", v, v)
			}
		},
	}
}

var jsonResolver = ScalarResolver{
	// serialize object to a JSON string when sending to clients
	Serialize: func(value any) (any, error) {
		bs, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("JSON scalar serialize error: %v", err)
		}
		return string(bs), nil
	},
	// parse JSON string from clients into the equivalent Go type (string, slice, map, etc.)
	ParseValue: func(value any) (any, error) {
		switch v := value.(type) {
		case string:
			if v == "" {
				return nil, nil
			}
			var x any
			if err := json.Unmarshal([]byte(v), &x); err != nil {
				return nil, fmt.Errorf("JSON scalar parse error: %v", err)
			}
			return x, nil
		default:
			return nil, fmt.Errorf("JSON scalar parse value unexpected type %T", v)
		}
	},
	ParseLiteral: func(valueAST ast.Value) (any, error) {
		switch v := valueAST.(type) {
		case *ast.StringValue:
			var jsonStr string
			if v != nil {
				jsonStr = v.Value
			}
			if jsonStr == "" {
				return nil, nil
			}
			var x any
			if err := json.Unmarshal([]byte(jsonStr), &x); err != nil {
				return nil, fmt.Errorf("JSON scalar parse literal error: %v", err)
			}
			return x, nil
		default:
			return nil, fmt.Errorf("JSON scalar parse literal unexpected type %T", v)
		}
	},
}

var voidScalarResolver = ScalarResolver{
	Serialize: func(value any) (any, error) {
		if value != nil {
			return nil, fmt.Errorf("void scalar serialize unexpected value: %v", value)
		}
		return nil, nil
	},
	ParseValue: func(value any) (any, error) {
		if value != nil {
			return nil, fmt.Errorf("void scalar parse value unexpected value: %v", value)
		}
		return nil, nil
	},
	ParseLiteral: func(valueAST ast.Value) (any, error) {
		if valueAST == nil {
			return nil, nil
		}
		if valueAST.GetValue() != nil {
			return nil, fmt.Errorf("void scalar parse literal unexpected value: %v", valueAST.GetValue())
		}
		return nil, nil
	},
}

func ToVoidResolver[P any, A any](f func(context.Context, P, A) error) graphql.FieldResolveFn {
	return ToResolver(func(ctx context.Context, p P, a A) (any, error) {
		return nil, f(ctx, p, a)
	})
}
