package core

import (
	"fmt"

	"github.com/graphql-go/graphql"
	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/router"
)

var _ router.ExecutableSchema = &gitSchema{}

type gitSchema struct {
	*baseSchema
}

func (s *gitSchema) Name() string {
	return "git"
}

func (s *gitSchema) Schema() string {
	return `
	extend type Query {
		"Query a git repository"
		git(url: String!): GitRepository!
	}
	
	"A git repository"
	type GitRepository {
		"List of branches on the repository"
		branches: [String!]!
		"Details on one branch"
		branch(name: String!): GitRef!
		"List of tags on the repository"
		tags: [String!]!
		"Details on one tag"
		tag(name: String!): GitRef!
	}
	
	"A git ref (tag or branch)"
	type GitRef {
		"The digest of the current value of this ref"
		digest: String!
		"The filesystem tree at this ref"
		tree: Filesystem!
	}

	# Compat with old API
	extend type Core {
		git(remote: String!, ref: String): Filesystem! @deprecated(reason: "use top-level 'query { git }'")
	}
`
}

func (s *gitSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"git": s.git,
		},
		"GitRepository": router.ObjectResolver{
			"branches": s.branches,
			"branch":   s.branch,
			"tags":     s.tags,
			"tag":      s.tag,
		},
		"GitRef": router.ObjectResolver{
			"digest": s.digest,
			"tree":   s.tree,
		},
		"Core": router.ObjectResolver{
			"git": s.gitOld,
		},
	}
}

func (s *gitSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

// Compat with old git API
func (s *gitSchema) gitOld(p graphql.ResolveParams) (any, error) {
	remote := p.Args["remote"].(string)
	ref, _ := p.Args["ref"].(string)

	var opts []llb.GitOption
	if s.sshAuthSockID != "" {
		opts = append(opts, llb.MountSSHSock(s.sshAuthSockID))
	}
	st := llb.Git(remote, ref, opts...)
	return s.Solve(p.Context, st)
}

type gitRepository struct {
	url string
}

type gitRef struct {
	repository gitRepository
	name       string
}

func (s *gitSchema) git(p graphql.ResolveParams) (any, error) {
	url := p.Args["url"].(string)

	return gitRepository{
		url: url,
	}, nil
}

func (s *gitSchema) branch(p graphql.ResolveParams) (any, error) {
	repo := p.Source.(gitRepository)
	return gitRef{
		repository: repo,
		name:       p.Args["name"].(string),
	}, nil
}

func (s *gitSchema) branches(p graphql.ResolveParams) (any, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *gitSchema) tag(p graphql.ResolveParams) (any, error) {
	repo := p.Source.(gitRepository)
	return gitRef{
		repository: repo,
		name:       p.Args["name"].(string),
	}, nil
}

func (s *gitSchema) tags(p graphql.ResolveParams) (any, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *gitSchema) digest(p graphql.ResolveParams) (any, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *gitSchema) tree(p graphql.ResolveParams) (any, error) {
	ref := p.Source.(gitRef)
	var opts []llb.GitOption
	if s.sshAuthSockID != "" {
		opts = append(opts, llb.MountSSHSock(s.sshAuthSockID))
	}
	st := llb.Git(ref.repository.url, ref.name, opts...)
	return s.Solve(p.Context, st)
}
