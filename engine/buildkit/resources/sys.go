package resources

import resourcestypes "github.com/dagger/dagger/engine/buildkit/resources/types"

type SysSampler = Sub[*resourcestypes.SysSample]

func NewSysSampler() (*Sampler[*resourcestypes.SysSample], error) {
	return newSysSampler()
}
