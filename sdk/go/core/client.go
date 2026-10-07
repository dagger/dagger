package core

import (
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

// initRoot uses the default transport without opening a connection. Object
// construction stays lazy, and closing the default session permits reconnecting.
func initRoot() *Query {
	return &Query{query: querybuilder.Query().Client(dagger.DefaultGraphQLClient())}
}

// Close closes the shared default engine connection.
func Close() error { return dagger.Close() }
