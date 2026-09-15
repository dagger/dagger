package core

import (
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

// RegistryProtocol is a GraphQL enum type.
type RegistryProtocol string

var RegistryProtocols = dagql.NewEnum[RegistryProtocol]()

var (
	RegistryProtocolHTTPS = RegistryProtocols.Register("HTTPS")
	RegistryProtocolHTTP  = RegistryProtocols.Register("HTTP")
)

func (proto RegistryProtocol) Type() *ast.Type {
	return &ast.Type{
		NamedType: "RegistryProtocol",
		NonNull:   true,
	}
}

func (proto RegistryProtocol) TypeDescription() string {
	return "Transport protocol to use for registry operations."
}

func (proto RegistryProtocol) Decoder() dagql.InputDecoder {
	return RegistryProtocols
}

func (proto RegistryProtocol) ToLiteral() call.Literal {
	return RegistryProtocols.Literal(proto)
}

func (proto RegistryProtocol) Scheme() string {
	return strings.ToLower(string(proto))
}
