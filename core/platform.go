package core

import (
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"
)

type Platform specs.Platform

func (p Platform) Spec() specs.Platform {
	return specs.Platform(p)
}

func (p Platform) Format() string {
	return platforms.Format(specs.Platform(p))
}

var _ dagql.Typed = Platform{}

func (p Platform) TypeName() string {
	return "Platform"
}

func (p Platform) TypeDescription() string {
	return dagql.FormatDescription(
		`The platform config OS and architecture in a Container.`,
		`The format is [os]/[platform]/[version] (e.g., "darwin/arm64/v7", "windows/amd64", "linux/arm64").`)
}

func (p Platform) Type() *ast.Type {
	return &ast.Type{
		NamedType: p.TypeName(),
		NonNull:   true,
	}
}

var _ dagql.Input = Platform{}

func (p Platform) Decoder() dagql.InputDecoder {
	return p
}

func (p Platform) ToLiteral() idproto.Literal {
	return idproto.NewLiteralString(platforms.Format(specs.Platform(p)))
}

var _ dagql.ScalarType = Platform{}

func (Platform) DecodeInput(val any) (dagql.Input, error) {
	switch x := val.(type) {
	case string:
		plat, err := platforms.Parse(x)
		if err != nil {
			return nil, err
		}
		return Platform(plat), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Platform", val)
	}
}

func (p Platform) MarshalJSON() ([]byte, error) {
	return json.Marshal(platforms.Format(specs.Platform(p)))
}
