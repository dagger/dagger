package core

import (
	"fmt"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
)

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
	*baseSchema
}

func (s *filesystemSchema) Schema() string {
	return `
	scalar FSID

	"""
	A reference to a filesystem tree.
	For example:
	 - The root filesystem of a container
	 - A source code repository
	 - A directory containing binary artifacts
	Rule of thumb: if it fits in a tar archive, it fits in a Filesystem.
	"""
	type Filesystem {
		id: FSID!

		"read a file at path"
		file(path: String!, lines: Int): String

		# FIXME: this should be in execSchema. However, removing this results in an error:
		# failed to resolve all type definitions: [Core Query Filesystem Exec]
		"execute a command inside this filesystem"
		exec(input: ExecInput!): Exec!
	}

	extend type Core {
		"Look up a filesystem by its ID"
		filesystem(id: FSID!): Filesystem!
	}
	`
}

func (s *filesystemSchema) Operations() string {
	return ""
}

func (s *filesystemSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"FSID": fsIDResolver,
		"Core": router.ObjectResolver{
			"filesystem": s.filesystem,
		},
		"Filesystem": router.ObjectResolver{
			"file": s.file,
		},
	}
}

func (s *filesystemSchema) filesystem(p graphql.ResolveParams) (any, error) {
	return filesystem.New(p.Args["id"].(filesystem.FSID)), nil
}

func (s *filesystemSchema) file(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	path := p.Args["path"].(string)

	output, err := obj.ReadFile(p.Context, s.gw, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return truncate(string(output), p.Args), nil
}
