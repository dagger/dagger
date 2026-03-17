package client

import (
	"context"

	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	apitypes "github.com/dagger/dagger/internal/buildkit/api/types"
	"github.com/pkg/errors"
)

type Info struct {
	BuildkitVersion BuildkitVersion `json:"buildkitVersion"`
	SystemInfo      SystemInfo      `json:"systemInfo"`
}

type BuildkitVersion struct {
	Package  string `json:"package"`
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

type SystemInfo struct {
	NumCPU int `json:"numCPU"`
}

func (c *Client) Info(ctx context.Context) (*Info, error) {
	res, err := c.ControlClient().Info(ctx, &controlapi.InfoRequest{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to call info")
	}
	return &Info{
		BuildkitVersion: fromAPIBuildkitVersion(res.BuildkitVersion),
		SystemInfo:      fromAPISystemInfo(res.SystemInfo),
	}, nil
}

func fromAPIBuildkitVersion(in *apitypes.BuildkitVersion) BuildkitVersion {
	if in == nil {
		return BuildkitVersion{}
	}
	return BuildkitVersion{
		Package:  in.Package,
		Version:  in.Version,
		Revision: in.Revision,
	}
}

func fromAPISystemInfo(in *controlapi.SystemInfo) SystemInfo {
	if in == nil {
		return SystemInfo{}
	}
	return SystemInfo{
		NumCPU: int(in.NumCPU),
	}
}
