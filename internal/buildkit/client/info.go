package client

import (
	"context"
	"time"

	controlapi "github.com/dagger/dagger/internal/buildkit/api/services/control"
	apitypes "github.com/dagger/dagger/internal/buildkit/api/types"
	"github.com/dagger/dagger/internal/buildkit/util/grpcerrors"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
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

func (c *Client) WaitInfo(ctx context.Context) (*Info, error) {
	for {
		res, err := c.ControlClient().Info(ctx, &controlapi.InfoRequest{})
		if err == nil {
			return &Info{
				BuildkitVersion: fromAPIBuildkitVersion(res.BuildkitVersion),
				SystemInfo:      fromAPISystemInfo(res.SystemInfo),
			}, nil
		}

		switch code := grpcerrors.Code(err); code {
		case codes.Unavailable:
		case codes.Unimplemented:
			// only buildkit v0.11+ supports the info api, which was used starting dagger v0.3.8.
			return nil, errors.Wrap(err, "version is too old, please upgrade dagger engine")
		default:
			return nil, errors.Wrap(err, "failed to call info")
		}

		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-time.After(time.Second):
		}
		c.conn.ResetConnectBackoff()
	}
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
