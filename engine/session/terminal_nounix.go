//go:build !unix
// +build !unix

package session

import (
	context "context"
	"io"
)

func (s TerminalAttachable) listenForResize(ctx context.Context, srv Terminal_SessionServer, stdout io.Writer) {
}
