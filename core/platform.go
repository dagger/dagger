package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
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
		// Parse keeps some spellings of one platform apart, such as
		// "linux/arm64/v8" and "linux/arm64". Normalize, as the engine does
		// wherever it resolves or compares platforms, so that each platform
		// has one value, and a call that takes one has one key.
		return Platform(platforms.Normalize(plat)), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Platform", val)
	}
}

func (p Platform) MarshalJSON() ([]byte, error) {
	return json.Marshal(platforms.Format(specs.Platform(p)))
}

func (p *Platform) UnmarshalJSON(payload []byte) error {
	var raw string
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	decoded, err := Platform{}.DecodeInput(raw)
	if err != nil {
		return err
	}
	plat, ok := decoded.(Platform)
	if !ok {
		return fmt.Errorf("platform JSON decoded to unexpected type %T", decoded)
	}
	*p = plat
	return nil
}

// EngineDefaultPlatformInput names the implicit input that gives a call the
// platform of the engine that made it.
const EngineDefaultPlatformInput = "engineDefaultPlatform"

// DeclaredPlatformDigest is the identity of the result of frame declared as
// made for platform (dagql.Cache.PlatformDigest), with what its inputs know
// of their contents now.
func DeclaredPlatformDigest(ctx context.Context, frame *dagql.ResultCall, platform Platform) (digest.Digest, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return "", err
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return "", err
	}
	dig, err := cache.PlatformDigest(frame, platform.Format(), dagql.BuildPlatform{
		ImplicitInput: EngineDefaultPlatformInput,
		IsPlatformArg: func(receiverType, field string, view call.View, arg string) bool {
			typ, ok := srv.ObjectType(receiverType)
			if !ok {
				return false
			}
			spec, ok := typ.FieldSpec(field, view)
			if !ok {
				return false
			}
			input, ok := spec.Args.Input(arg, view)
			return ok && input.Type.Type().Name() == (Platform{}).TypeName()
		},
		IsCommand: func(receiverType, field string) bool {
			return receiverType == (&Container{}).Type().Name() && field == "withExec"
		},
	})
	if err != nil {
		return "", fmt.Errorf("identify the result declared for %s: %w", platform.Format(), err)
	}
	return dig, nil
}
