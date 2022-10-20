package schema

import (
	"errors"
	"fmt"

	"github.com/dagger/dagger/router"
	"github.com/graphql-go/graphql/language/ast"
)

// ErrNotImplementedYet is used to stub out API fields that aren't implemented
// yet.
var ErrNotImplementedYet = errors.New("not implemented yet")

// stringResolver is used to generate a scalar resolver for a stringable type.
func stringResolver[T ~string](sample T) router.ScalarResolver {
	return router.ScalarResolver{
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
