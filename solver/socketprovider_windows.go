//go:build windows
// +build windows

package solver

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

func dialStream(id string) (net.Conn, error) {
	if !strings.HasPrefix(id, npipePrefix) {
		return nil, fmt.Errorf("invalid socket forward key %s", id)
	}

	id = strings.TrimPrefix(id, npipePrefix)
	dur := time.Second
	return winio.DialPipe(id, &dur)
}
