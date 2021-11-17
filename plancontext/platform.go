package plancontext

import (
	"github.com/containerd/containerd/platforms"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	// Default platform.
	// FIXME: This should be auto detected using buildkit
	defaultPlatform = specs.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
)

type platformContext struct {
	platform specs.Platform
}

func (c *platformContext) Get() specs.Platform {
	return c.platform
}

func (c *platformContext) Set(platform string) error {
	p, err := platforms.Parse(platform)
	if err != nil {
		return err
	}
	c.platform = p
	return nil
}
