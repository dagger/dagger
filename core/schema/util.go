package schema

import (
	"fmt"

	"github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
	"github.com/dagger/graphql/language/ast"
)

var ErrServicesDisabled = fmt.Errorf("services are disabled; unset %s to enable", engine.ServicesDNSEnvName)

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
