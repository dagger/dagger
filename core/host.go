package core

import (
	"context"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

type Host struct {
	Query *Query
}

func (*Host) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Host",
		NonNull:   true,
	}
}

func (*Host) TypeDescription() string {
	return "Information about the host environment."
}

func (host *Host) SetSecretFile(ctx context.Context, srv *dagql.Server, secretName string, path string) (i dagql.Instance[*Secret], err error) {
	secretStore, err := host.Query.Secrets(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get secrets: %w", err)
	}

	accessor, err := GetClientResourceAccessor(ctx, host.Query, secretName)
	if err != nil {
		return i, err
	}

	bk, err := host.Query.Buildkit(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	secretFileContent, err := bk.ReadCallerHostFile(ctx, path)
	if err != nil {
		return i, fmt.Errorf("read secret file: %w", err)
	}

	err = srv.Select(ctx, srv.Root(), &i, dagql.Selector{
		Field: "secret",
		Args: []dagql.NamedInput{
			{
				Name:  "name",
				Value: dagql.NewString(secretName),
			},
			{
				Name:  "accessor",
				Value: dagql.Opt(dagql.NewString(accessor)),
			},
		},
	})
	if err != nil {
		return i, fmt.Errorf("failed to select secret: %w", err)
	}

	if err := secretStore.AddSecret(i.Self, secretName, secretFileContent); err != nil {
		return i, fmt.Errorf("failed to add secret: %w", err)
	}

	return i, nil
}
