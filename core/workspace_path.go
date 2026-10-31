package core

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

// WorkspacePath uses workspace coordinates, never host filesystem coordinates.
// Input paths remain relative until an operation resolves them against its cwd.
// Source selectors return absolute paths within the workspace boundary.
type WorkspacePath string

var _ dagql.ScalarType = WorkspacePath("")

func (WorkspacePath) TypeName() string { return "WorkspacePath" }
func (WorkspacePath) TypeDescription() string {
	return "A path within a workspace. A leading slash starts at the workspace boundary. Other input paths start at the operation's working directory. Output source paths start at the workspace boundary."
}
func (p WorkspacePath) Type() *ast.Type {
	return &ast.Type{NamedType: p.TypeName(), NonNull: true}
}
func (p WorkspacePath) Decoder() dagql.InputDecoder { return p }
func (p WorkspacePath) ToLiteral() call.Literal     { return call.NewLiteralString(string(p)) }
func (WorkspacePath) DecodeInput(value any) (dagql.Input, error) {
	s, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("cannot convert %T to WorkspacePath", value)
	}
	if strings.ContainsRune(s, '\x00') {
		return nil, fmt.Errorf("workspace path contains a null byte")
	}
	return WorkspacePath(path.Clean(strings.ReplaceAll(s, `\`, "/"))), nil
}
func (p WorkspacePath) MarshalJSON() ([]byte, error) { return json.Marshal(string(p)) }
func (p *WorkspacePath) UnmarshalJSON(payload []byte) error {
	var s string
	if err := json.Unmarshal(payload, &s); err != nil {
		return err
	}
	value, err := WorkspacePath("").DecodeInput(s)
	if err != nil {
		return err
	}
	*p = value.(WorkspacePath)
	return nil
}
