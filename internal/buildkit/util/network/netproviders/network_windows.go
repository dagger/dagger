//go:build windows
// +build windows

package netproviders

import (
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/network"
)

func getHostProvider() (network.Provider, bool) {
	return nil, false
}

func getFallback() (network.Provider, string) {
	bklog.L.Warn("using null network as the default")
	return network.NewNoneProvider(), ""
}
