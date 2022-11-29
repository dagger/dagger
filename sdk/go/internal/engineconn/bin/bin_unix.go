//go:build !windows

package bin

import (
	"context"
	"net"
)

func dialer(addr string) Dialer {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("unix", addr)
	}
}
