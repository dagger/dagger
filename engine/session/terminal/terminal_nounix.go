//go:build !unix
// +build !unix

package terminal

import (
	context "context"
	"io"
)

func (s TerminalAttachable) listenForResize(ctx context.Context, srv Terminal_SessionServer, stdout io.Writer) {
}
