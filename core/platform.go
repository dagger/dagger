package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/platforms"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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

func (p Platform) ToLiteral() call.Literal {
	return call.NewLiteralString(platforms.Format(specs.Platform(p)))
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

func (p *Platform) UnmarshalJSON(bs []byte) error {
	var s string
	if err := json.Unmarshal(bs, &s); err != nil {
		return err
	}
	plat, err := platforms.Parse(s)
	if err != nil {
		return err
	}
	*p = Platform(plat)
	return nil
}

func (Platform) FromJSON(ctx context.Context, bs []byte) (dagql.Typed, error) {
	var p Platform
	if err := p.UnmarshalJSON(bs); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w", err)
	}
	return p, nil
}

func (p Platform) ToResult(ctx context.Context, srv *dagql.Server) (dagql.Result, error) {
	resultID, resultDgst, err := srv.ScalarResult(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("scalar result: %w", err)
	}
	return dagql.NewInputResult(resultID, resultDgst.String(), p), nil
}
