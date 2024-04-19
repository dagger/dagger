//go:build windows
// +build windows

package main

import (
	"crypto/tls"
	"net"

	"github.com/Microsoft/go-winio"
	_ "github.com/moby/buildkit/solver/llbsolver/ops"
	_ "github.com/moby/buildkit/util/system/getuserinfo"
	"github.com/pkg/errors"
)

const socketScheme = "npipe://"

func listenFD(addr string, tlsConfig *tls.Config) (net.Listener, error) {
	return nil, errors.New("listening server on fd not supported on windows")
}

func getLocalListener(listenerPath string) (net.Listener, error) {
	pc := &winio.PipeConfig{
		// Allow generic read and generic write access to authenticated users
		// and system users. On Linux, this pipe seems to be given rw access to
		// user, group and others (666).
		// TODO(gabriel-samfira): should we restrict access to this pipe to just
		// authenticated users? Or Administrators group?
		SecurityDescriptor: "D:P(A;;GRGW;;;AU)(A;;GRGW;;;SY)",
	}

	listener, err := winio.ListenPipe(listenerPath, pc)
	if err != nil {
		return nil, errors.Wrap(err, "creating listener")
	}
	return listener, nil
}
