package schema

import (
	"fmt"

	"github.com/dagger/graphql/language/ast"
)

// stringResolver is used to generate a scalar resolver for a stringable type.
func stringResolver[T ~string](sample T) ScalarResolver {
	return ScalarResolver{
		Serialize: func(value any) any {
			switch v := value.(type) {
			case string, T:
				return v
			default:
				panic(fmt.Sprintf("unexpected %T type %T", sample, v))
			}
		},
		ParseValue: func(value any) any {
			switch v := value.(type) {
			case string:
				return T(v)
			default:
				panic(fmt.Sprintf("unexpected %T value type %T: %+v", sample, v, v))
			}
		},
		ParseLiteral: func(valueAST ast.Value) any {
			switch valueAST := valueAST.(type) {
			case *ast.StringValue:
				return T(valueAST.Value)
			default:
				panic(fmt.Sprintf("unexpected %T literal type: %T", sample, valueAST))
			}
		},
	}
}
