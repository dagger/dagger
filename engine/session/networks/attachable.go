package networks

import (
	context "context"

	"github.com/moby/buildkit/session"
	"google.golang.org/grpc"
)

type ConfigFunc func(string) *Config

type attachable struct {
	getConfig ConfigFunc
}

func NewConfigProvider(getConfig ConfigFunc) session.Attachable {
	return &attachable{
		getConfig: getConfig,
	}
}

func (a *attachable) Register(srv *grpc.Server) {
	RegisterNetworksServer(srv, a)
}

func (a *attachable) GetNetwork(ctx context.Context, req *GetNetworkRequest) (*GetNetworkResponse, error) {
	return &GetNetworkResponse{
		Config: a.getConfig(req.GetID()), // may be nil
	}, nil
}
