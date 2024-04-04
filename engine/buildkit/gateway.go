package buildkit

import (
	"context"
	"sync"

	"github.com/gogo/protobuf/proto"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

const (
	FocusPrefix    = "[focus] "
	InternalPrefix = "[internal] "
)

type opTrackingGateway struct {
	llbBridge frontend.FrontendLLBBridge

	ops   map[digest.Digest]proto.Message
	opsMu sync.Mutex
}

var _ frontend.FrontendLLBBridge = &opTrackingGateway{}

// ResolveImageConfig calls the inner ResolveImageConfig.
func (g *opTrackingGateway) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (string, digest.Digest, []byte, error) {
	return g.llbBridge.ResolveImageConfig(ctx, ref, opt)
}

// Solve records the vertexes of the definition and frontend inputs as members
// of the current progress group, and calls the inner Solve.
func (g *opTrackingGateway) Solve(ctx context.Context, req frontend.SolveRequest, sessionID string) (*frontend.Result, error) {
	if req.Definition != nil {
		g.opsMu.Lock()
		if g.ops == nil {
			g.ops = make(map[digest.Digest]proto.Message)
		}
		for _, dt := range req.Definition.Def {
			dgst := digest.FromBytes(dt)
			if _, ok := g.ops[dgst]; ok {
				continue
			}
			var op pb.Op
			if err := (&op).Unmarshal(dt); err != nil {
				g.opsMu.Unlock()
				return nil, err
			}

			// remove raw file contents (these can be kinda large)
			if fileOp := op.GetFile(); fileOp != nil {
				for _, action := range fileOp.Actions {
					if mkfile := action.GetMkfile(); mkfile != nil {
						mkfile.Data = nil
					}
				}
			}

			switch op := op.Op.(type) {
			case *pb.Op_Exec:
				g.ops[dgst] = op.Exec
			case *pb.Op_Source:
				g.ops[dgst] = op.Source
			case *pb.Op_File:
				g.ops[dgst] = op.File
			case *pb.Op_Build:
				g.ops[dgst] = op.Build
			case *pb.Op_Merge:
				g.ops[dgst] = op.Merge
			case *pb.Op_Diff:
				g.ops[dgst] = op.Diff
			}
		}
		g.opsMu.Unlock()
	}

	for _, input := range req.FrontendInputs {
		if input == nil {
			// TODO(vito): we currently pass a nil def to Dockerfile inputs, should
			// probably change that to llb.Scratch
			continue
		}
	}

	return g.llbBridge.Solve(ctx, req, sessionID)
}

func (g *opTrackingGateway) Warn(ctx context.Context, dgst digest.Digest, msg string, opts frontend.WarnOpts) error {
	return g.llbBridge.Warn(ctx, dgst, msg, opts)
}
