package schema

import (
	"fmt"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/router"
	"github.com/dagger/graphql/language/ast"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type platformSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &platformSchema{}

func (s *platformSchema) Name() string {
	return "platform"
}

func (s *platformSchema) Schema() string {
	return Platform
}

func (s *platformSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"defaultPlatform": router.ToResolver(s.defaultPlatform),
		},
		"Platform": router.ScalarResolver{
			Serialize: func(value any) any {
				switch v := value.(type) {
				case specs.Platform:
					return platforms.Format(v)
				case *specs.Platform:
					if v == nil {
						return ""
					}
					return platforms.Format(*v)
				default:
					panic(fmt.Sprintf("unexpected platform scalar serialize type %T", v))
				}
			},
			ParseValue: func(value any) any {
				switch v := value.(type) {
				case string:
					p, err := platforms.Parse(v)
					if err != nil {
						panic(router.InvalidInputError{Err: fmt.Errorf("platform parse value error: %w", err)})
					}
					return p
				default:
					panic(fmt.Sprintf("unexpected platform parse value type %T: %+v", v, v))
				}
			},
			ParseLiteral: func(valueAST ast.Value) any {
				switch valueAST := valueAST.(type) {
				case *ast.StringValue:
					p, err := platforms.Parse(valueAST.Value)
					if err != nil {
						panic(router.InvalidInputError{Err: fmt.Errorf("platform parse literal error: %w", err)})
					}
					return p
				default:
					panic(fmt.Sprintf("unexpected platform parse literal type: %T: %+v", valueAST, valueAST))
				}
			},
		},
	}
}

func (s *platformSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (s *platformSchema) defaultPlatform(ctx *router.Context, parent, args any) (specs.Platform, error) {
	return s.baseSchema.platform, nil
}
