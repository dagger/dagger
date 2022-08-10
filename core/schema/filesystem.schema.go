package core

import (
	"fmt"

	"github.com/dagger/cloak/core"
	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
)

func init() {
	core.Register("filesystem", func(base *core.BaseSchema) router.ExecutableSchema { return &filesystemSchema{base} })
}

var fsIDResolver = router.ScalarResolver{
	Serialize: func(value any) any {
		switch v := value.(type) {
		case filesystem.FSID, string:
			return v
		default:
			panic(fmt.Sprintf("unexpected fsid type %T", v))
		}
	},
	ParseValue: func(value any) any {
		switch v := value.(type) {
		case string:
			return filesystem.FSID(v)
		default:
			panic(fmt.Sprintf("unexpected fsid value type %T: %+v", v, v))
		}
	},
	ParseLiteral: func(valueAST ast.Value) any {
		switch valueAST := valueAST.(type) {
		case *ast.StringValue:
			return filesystem.FSID(valueAST.Value)
		default:
			panic(fmt.Sprintf("unexpected fsid literal type: %T", valueAST))
		}
	},
}

var _ router.ExecutableSchema = &filesystemSchema{}

type filesystemSchema struct {
	*core.BaseSchema
}

func (s *filesystemSchema) Schema() string {
	return `
	scalar FSID

	type Filesystem {
		id: FSID!
		file(path: String!, lines: Int): String

		# FIXME: this should be in execSchema. However, removing this results in an error:
		# failed to resolve all type definitions: [Core Query Filesystem Exec]
		exec(input: ExecInput!): Exec!
	}

	extend type Core {
		filesystem(id: FSID!): Filesystem!
	}
	`
}

func (s *filesystemSchema) Operations() string {
	return ""
}

func (r *filesystemSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"FSID": fsIDResolver,
		"Core": router.ObjectResolver{
			"filesystem": r.filesystem,
		},
		"Filesystem": router.ObjectResolver{
			"file": r.file,
		},
	}
}

func (r *filesystemSchema) filesystem(p graphql.ResolveParams) (any, error) {
	return filesystem.New(p.Args["id"].(filesystem.FSID)), nil
}

func (r *filesystemSchema) file(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	path := p.Args["path"].(string)

	output, err := obj.ReadFile(p.Context, r.Gateway, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return truncate(string(output), p.Args), nil
}
