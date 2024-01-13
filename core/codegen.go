package core

import (
	"context"

	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"
)

type GeneratedCode struct {
	Code              *Directory `field:"true" doc:"The directory containing the generated code."`
	VCSIgnoredPaths   []string   `field:"true" name:"vcsIgnoredPaths" doc:"List of paths to ignore in version control (i.e. .gitignore)."`
	VCSGeneratedPaths []string   `field:"true" name:"vcsGeneratedPaths" doc:"List of paths to mark generated in version control (i.e. .gitattributes)."`
}

func NewGeneratedCode(code *Directory) *GeneratedCode {
	return &GeneratedCode{
		Code: code,
	}
}

func (*GeneratedCode) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GeneratedCode",
		NonNull:   true,
	}
}

func (*GeneratedCode) TypeDescription() string {
	return "The result of running an SDK's codegen."
}

func (code GeneratedCode) Clone() *GeneratedCode {
	cp := code
	if cp.Code != nil {
		cp.Code = cp.Code.Clone()
	}
	return &cp
}

func (code *GeneratedCode) WithVCSIgnoredPaths(paths []string) *GeneratedCode {
	code = code.Clone()
	code.VCSIgnoredPaths = paths
	return code
}

func (code *GeneratedCode) WithVCSGeneratedPaths(paths []string) *GeneratedCode {
	code = code.Clone()
	code.VCSGeneratedPaths = paths
	return code
}

var _ HasPBDefinitions = (*GeneratedCode)(nil)

func (code *GeneratedCode) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return code.Code.PBDefinitions(ctx)
}
