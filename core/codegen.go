package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

type GeneratedCode struct {
	Code dagql.ObjectResult[*Directory] `field:"true" doc:"The directory containing the generated code."`

	VCSGeneratedPaths []string `field:"true" name:"vcsGeneratedPaths" doc:"List of paths to mark generated in version control (i.e. .gitattributes)."`
	VCSIgnoredPaths   []string `field:"true" name:"vcsIgnoredPaths" doc:"List of paths to ignore in version control (i.e. .gitignore)."`
}

var _ dagql.PersistedObject = (*GeneratedCode)(nil)
var _ dagql.PersistedObjectDecoder = (*GeneratedCode)(nil)
var _ dagql.HasDependencyResults = (*GeneratedCode)(nil)

type persistedGeneratedCodePayload struct {
	CodeResultID      uint64   `json:"codeResultID"`
	VCSGeneratedPaths []string `json:"vcsGeneratedPaths,omitempty"`
	VCSIgnoredPaths   []string `json:"vcsIgnoredPaths,omitempty"`
}

func (code *GeneratedCode) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if code == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted generated code: nil generated code")
	}
	if code.Code.Self() == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted generated code: missing code directory")
	}

	codeID, err := encodePersistedObjectRef(cache, code.Code, "generated code directory")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}

	payloadJSON, err := json.Marshal(persistedGeneratedCodePayload{
		CodeResultID:      codeID,
		VCSGeneratedPaths: slices.Clone(code.VCSGeneratedPaths),
		VCSIgnoredPaths:   slices.Clone(code.VCSIgnoredPaths),
	})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted generated code payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payloadJSON), nil
}

func (*GeneratedCode) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedGeneratedCodePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted generated code payload: %w", err)
	}
	if persisted.CodeResultID == 0 {
		return nil, fmt.Errorf("decode persisted generated code: missing code directory")
	}

	codeDir, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.CodeResultID, "generated code directory")
	if err != nil {
		return nil, err
	}

	return &GeneratedCode{
		Code:              codeDir,
		VCSGeneratedPaths: slices.Clone(persisted.VCSGeneratedPaths),
		VCSIgnoredPaths:   slices.Clone(persisted.VCSIgnoredPaths),
	}, nil
}

// AttachDependencyResults exposes the embedded Code directory as an owned
// dependency. This wires GeneratedCode -> Code into the cache liveness graph
// and lets failures in Code's lazy work (e.g. uv lock during Python codegen)
// be attributed back to the API span that returned this GeneratedCode.
func (code *GeneratedCode) AttachDependencyResults(
	ctx context.Context,
	self dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if code == nil || code.Code.Self() == nil {
		return nil, nil
	}
	attached, err := attach(code.Code)
	if err != nil {
		return nil, fmt.Errorf("attach generated code directory: %w", err)
	}
	dir, ok := attached.(dagql.ObjectResult[*Directory])
	if !ok {
		return nil, fmt.Errorf("attach generated code directory: unexpected result %T", attached)
	}
	code.Code = dir
	return []dagql.AnyResult{dir}, nil
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
