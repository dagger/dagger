package schema

import (
	"context"
	"slices"

	"github.com/dagger/dagger/core"
)

func (s *gitSchema) withPushURLs(_ context.Context, parent *core.GitRepository, args struct {
	URLs []string `name:"urls"`
}) (*core.GitRepository, error) {
	repo := parent.CloneWithBackend(parent.Backend)
	repo.PushURLs = slices.Clone(args.URLs)
	return repo, nil
}
