package plancontext

import (
	"github.com/containerd/containerd/platforms"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type platformContext struct {
	platform *specs.Platform
}

func (c *platformContext) Get() specs.Platform {
	return *c.platform
}

func (c *platformContext) SetString(platform string) error {
	p, err := platforms.Parse(platform)
	if err != nil {
		return err
	}
	c.platform = &p
	return nil
}

func (c *platformContext) Set(p specs.Platform) {
	c.platform = &p
}

func (c *platformContext) IsSet() bool {
	return c.platform != nil
}
