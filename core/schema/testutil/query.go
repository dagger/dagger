package testutil

import (
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/stretchr/testify/require"
)

func Query(t *testing.T, query string, variables any, res any) {
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		require.NoError(t, err)

		err = cl.MakeRequest(ctx,
			&graphql.Request{
				Query:     query,
				Variables: variables,
			},
			&graphql.Response{Data: &res},
		)
		require.NoError(t, err)

		return nil
	}))
}
