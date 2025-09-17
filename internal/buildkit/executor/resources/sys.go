package resources

import resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"

type SysSampler = Sub[*resourcestypes.SysSample]

func NewSysSampler() (*Sampler[*resourcestypes.SysSample], error) {
	return newSysSampler()
}
