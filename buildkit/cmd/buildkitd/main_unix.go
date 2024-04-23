//go:build !windows
// +build !windows

package main

import (
	"crypto/tls"
	"net"
	"os"
	"syscall"

	"github.com/containerd/containerd/sys"
	"github.com/coreos/go-systemd/v22/activation"
	"github.com/pkg/errors"
)

const socketScheme = "unix://"

func init() {
	syscall.Umask(0)
}

func listenFD(addr string, tlsConfig *tls.Config) (net.Listener, error) {
	var (
		err       error
		listeners []net.Listener
	)
	// socket activation
	if tlsConfig != nil {
		listeners, err = activation.TLSListeners(tlsConfig)
	} else {
		listeners, err = activation.Listeners()
	}
	if err != nil {
		return nil, err
	}

	if len(listeners) == 0 {
		return nil, errors.New("no sockets found via socket activation: make sure the service was started by systemd")
	}

	// default to first fd
	if addr == "" {
		return listeners[0], nil
	}

	//TODO: systemd fd selection (default is 3)
	return nil, errors.New("not supported yet")
}

func getLocalListener(listenerPath string) (net.Listener, error) {
	uid := os.Getuid()
	l, err := sys.GetLocalListener(listenerPath, uid, uid)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(listenerPath, 0666); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
