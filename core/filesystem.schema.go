package core

import (
	"fmt"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
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

func (s *filesystemSchema) Name() string {
	return "filesystem"
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

	"copy from a filesystem"
	copy(
		from: FSID!,
		srcPath: String,
		destPath: String,
		include: [String!]
		exclude: [String!]
	): Filesystem

	"push a filesystem as an image to a registry"
	pushImage(ref: String!): Boolean!
}

extend type Core {
	"Look up a filesystem by its ID"
	filesystem(id: FSID!): Filesystem!
}
`
}

func (s *filesystemSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"FSID": fsIDResolver,
		"Core": router.ObjectResolver{
			"filesystem": s.filesystem,
		},
		"Filesystem": router.ObjectResolver{
			"file":      s.file,
			"copy":      s.copy,
			"pushImage": s.pushImage,
		},
	}
}

func (s *filesystemSchema) Dependencies() []router.ExecutableSchema {
	return nil
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

func (s *filesystemSchema) copy(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	st, err := obj.ToState()
	if err != nil {
		return nil, err
	}

	contents, err := filesystem.New(p.Args["from"].(filesystem.FSID)).ToState()
	if err != nil {
		return nil, err
	}

	src := p.Args["srcPath"].(string)
	dest := p.Args["destPath"].(string)
	include, _ := p.Args["include"].([]string)
	exclude, _ := p.Args["exclude"].([]string)

	st = st.File(llb.Copy(contents, src, dest, &llb.CopyInfo{
		CopyDirContentsOnly: true,
		CreateDestPath:      true,
		AllowWildcard:       true,
		IncludePatterns:     include,
		ExcludePatterns:     exclude,
	}))

	fs, err := s.Solve(p.Context, st)
	if err != nil {
		return nil, err
	}
	return fs, err
}

func (s *filesystemSchema) pushImage(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	ref, _ := p.Args["ref"].(string)
	if ref == "" {
		return nil, fmt.Errorf("ref is required for pushImage")
	}

	if err := s.Export(p.Context, obj, bkclient.ExportEntry{
		Type: bkclient.ExporterImage,
		Attrs: map[string]string{
			"name": ref,
			"push": "true",
		},
	}); err != nil {
		return nil, err
	}
	return true, nil
}
