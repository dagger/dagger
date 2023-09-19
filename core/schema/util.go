package schema

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/language/ast"
)

// stringResolver is used to generate a scalar resolver for a stringable type.
func stringResolver[T ~string](sample T) ScalarResolver {
	return ScalarResolver{
		Serialize: func(value any) (any, error) {
			switch v := value.(type) {
			case string, T:
				return v, nil
			default:
				return nil, fmt.Errorf("unexpected %T type %T", sample, v)
			}
		},
		ParseValue: func(value any) (any, error) {
			switch v := value.(type) {
			case string:
				return T(v), nil
			default:
				return nil, fmt.Errorf("unexpected %T type %T", sample, v)
			}
		},
		ParseLiteral: func(valueAST ast.Value) (any, error) {
			switch valueAST := valueAST.(type) {
			case *ast.StringValue:
				return T(valueAST.Value), nil
			default:
				return nil, fmt.Errorf("unexpected %T literal type: %T", sample, valueAST)
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

func ToVoidResolver[P any, A any](f func(*core.Context, P, A) error) graphql.FieldResolveFn {
	return ToResolver(func(ctx *core.Context, p P, a A) (any, error) {
		return nil, f(ctx, p, a)
	})
}
