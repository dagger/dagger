//go:build !linux
// +build !linux

package netproviders

import (
	"runtime"

	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/internal/buildkit/util/network/cniprovider"
	"github.com/pkg/errors"
)

func getBridgeProvider(_ cniprovider.Opt) (network.Provider, error) {
	return nil, errors.Errorf("bridge network is not supported on %s yet", runtime.GOOS)
}
