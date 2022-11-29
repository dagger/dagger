//go:build windows

package bin

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func dialer(addr string) Dialer {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return winio.DialPipeContext(ctx, addr)
	}
}
