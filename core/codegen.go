package core

import (
	"context"
	"slices"

	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

type GeneratedCode struct {
	Code dagql.ObjectResult[*Directory] `field:"true" doc:"The directory containing the generated code."`

	VCSGeneratedPaths []string `field:"true" name:"vcsGeneratedPaths" doc:"List of paths to mark generated in version control (i.e. .gitattributes)."`
	VCSIgnoredPaths   []string `field:"true" name:"vcsIgnoredPaths" doc:"List of paths to ignore in version control (i.e. .gitignore)."`
}

func NewGeneratedCode(code dagql.ObjectResult[*Directory]) *GeneratedCode {
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
	return &cp
}

func (code *GeneratedCode) WithVCSGeneratedPaths(paths []string) *GeneratedCode {
	code = code.Clone()
	code.VCSGeneratedPaths = paths
	return code
}

func (code *GeneratedCode) WithVCSIgnoredPaths(paths []string) *GeneratedCode {
	code = code.Clone()
	code.VCSIgnoredPaths = paths

	// if the paths does not have a .env file we need to add it
	if !slices.Contains(code.VCSIgnoredPaths, ".env") {
		code.VCSIgnoredPaths = append(code.VCSIgnoredPaths, ".env")
	}

	return code
}

var _ HasPBDefinitions = (*GeneratedCode)(nil)

func (code *GeneratedCode) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	return code.Code.Self().PBDefinitions(ctx)
}
