package core

import (
	"context"
	"sync"

	dagger "dagger.io/dagger"
	"github.com/dagger/querybuilder"
)

// NewQuery builds the root of the Dagger API query tree for an existing
// engine connection. Use it when you already have a *dagger.Client (for
// example from an explicit dagger.Connect call) and want to run queries
// against it via this package's generated types.
func NewQuery(client *dagger.Client) *Query {
	return &Query{
		query: querybuilder.Query().Client(client.GraphQLClient()),
	}
}

// QueryBuilder returns the underlying query builder.
func (r *Query) QueryBuilder() *querybuilder.Selection {
	return r.query
}

var (
	defaultRoot   *Query
	defaultRootMu sync.Mutex
)

// initRoot lazily connects to the engine using dagger.Default, the
// process-wide shared connection, and caches the resulting query root for
// this package's top-level functions (Container(), Directory(), etc.).
func initRoot() *Query {
	defaultRootMu.Lock()
	defer defaultRootMu.Unlock()

	if defaultRoot == nil {
		client, err := dagger.Default(context.Background())
		if err != nil {
			panic(err)
		}
		defaultRoot = NewQuery(client)
	}
	return defaultRoot
}

// Close closes the default engine connection used by this package's
// top-level functions.
func Close() error {
	defaultRootMu.Lock()
	defer defaultRootMu.Unlock()

	defaultRoot = nil
	return dagger.Close()
}
