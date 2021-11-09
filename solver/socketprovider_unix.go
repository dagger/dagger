//go:build !windows
// +build !windows

package solver

import (
	"fmt"
	"net"
	"strings"
	"time"
)

func dialStream(id string) (net.Conn, error) {
	if !strings.HasPrefix(id, unixPrefix) {
		return nil, fmt.Errorf("invalid socket forward key %s", id)
	}

	id = strings.TrimPrefix(id, unixPrefix)
	return net.DialTimeout("unix", id, time.Second)
}
