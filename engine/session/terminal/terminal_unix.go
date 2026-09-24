//go:build unix
// +build unix

package terminal

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func (s TerminalAttachable) listenForResize(ctx context.Context, sender *lockedSessionSender, stdout io.Writer) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	defer signal.Stop(sig)
	for {
		select {
		case <-ctx.Done():
			return
		case <-sig:
			s.sendSize(sender, stdout)
		}
	}
}
