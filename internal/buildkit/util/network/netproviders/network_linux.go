//go:build linux
// +build linux

package netproviders

import (
	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/internal/buildkit/util/network/cniprovider"
)

func getBridgeProvider(opt cniprovider.Opt) (network.Provider, error) {
	return cniprovider.NewBridge(opt)
}
