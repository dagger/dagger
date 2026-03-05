package core

import (
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// WorkspaceWriteSync controls how writes made through a workspace mount are
// synchronized back to the backing workspace.
type WorkspaceWriteSync string

var WorkspaceWriteSyncModes = dagql.NewEnum[WorkspaceWriteSync]()

var (
	WorkspaceWriteSyncEphemeral = WorkspaceWriteSyncModes.Register("EPHEMERAL",
		"Keep writes in Dagger state only; do not sync back to the host workspace")
	WorkspaceWriteSyncWriteThrough = WorkspaceWriteSyncModes.Register("WRITE_THROUGH",
		"Best-effort sync of workspace mount writes back to the host workspace")
)

func (mode WorkspaceWriteSync) Type() *ast.Type {
	return &ast.Type{
		NamedType: "WorkspaceWriteSync",
		NonNull:   true,
	}
}

func (mode WorkspaceWriteSync) TypeDescription() string {
	return "Controls how writes to a mounted workspace are synchronized."
}

func (mode WorkspaceWriteSync) Decoder() dagql.InputDecoder {
	return WorkspaceWriteSyncModes
}

func (mode WorkspaceWriteSync) ToLiteral() call.Literal {
	return WorkspaceWriteSyncModes.Literal(mode)
}
