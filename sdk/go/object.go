package dagger

import "context"

// Object is any Dagger object that can be identified by an ID.
// All code-generated types (e.g. *Container, *Directory) implement this interface.
type Object interface {
	// XXX_GraphQLType returns the GraphQL type name of the object.
	XXX_GraphQLType() string
	// XXX_GraphQLIDType returns the GraphQL ID type name of the object.
	XXX_GraphQLIDType() string
	// XXX_GraphQLID returns the object's ID as a string.
	XXX_GraphQLID(ctx context.Context) (string, error)
}
