//go:build !unix
// +build !unix

package terminal

import (
	context "context"
	"io"
)

func (s TerminalAttachable) listenForResize(ctx context.Context, sender *lockedSessionSender, stdout io.Writer) {
}
