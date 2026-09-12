package core

import (
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type GitPushDisposition string

var GitPushDispositions = dagql.NewEnum[GitPushDisposition]()
var (
	GitPushCreated     = GitPushDispositions.Register("CREATED", "The remote ref was created.")
	GitPushFastForward = GitPushDispositions.Register("FAST_FORWARD", "The remote ref was fast-forwarded.")
	GitPushForced      = GitPushDispositions.Register("FORCED", "The remote ref was replaced under an explicit lease.")
	GitPushUpToDate    = GitPushDispositions.Register("UP_TO_DATE", "The remote ref already pointed to this commit.")
)

func (GitPushDisposition) Type() *ast.Type {
	return &ast.Type{NamedType: "GitPushDisposition", NonNull: true}
}
func (GitPushDisposition) TypeDescription() string     { return "How a Git push updated the remote ref." }
func (GitPushDisposition) Decoder() dagql.InputDecoder { return GitPushDispositions }
func (d GitPushDisposition) ToLiteral() call.Literal   { return GitPushDispositions.Literal(d) }

type GitPushResult struct {
	Ref         string             `field:"true" doc:"The fully qualified remote ref."`
	PreviousSHA string             `field:"true" name:"previousSHA" doc:"The previous remote object ID; empty when the ref was created."`
	SHA         string             `field:"true" doc:"The object ID pushed to the remote."`
	Disposition GitPushDisposition `field:"true" doc:"How the remote ref was updated."`
}

func (*GitPushResult) Type() *ast.Type { return &ast.Type{NamedType: "GitPushResult", NonNull: true} }
func (*GitPushResult) TypeDescription() string {
	return "A receipt for a completed Git push. Reading or replaying the receipt does not push again."
}
