//go:build !linux

package resources

import resourcestypes "github.com/dagger/dagger/engine/buildkit/resources/types"

func newSysSampler() (*Sampler[*resourcestypes.SysSample], error) {
	return nil, nil
}
