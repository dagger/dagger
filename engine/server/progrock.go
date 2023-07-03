package server

import (
	"context"
	"net"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/core"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
)

type recordingGateway struct {
	llbBridge frontend.FrontendLLBBridge
}

// ResolveImageConfig records the image config resolution vertex as a member of
// the current progress group, and calls the inner ResolveImageConfig.
func (g recordingGateway) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (digest.Digest, []byte, error) {
	rec := progrock.RecorderFromContext(ctx)

	// HACK(vito): this is how Buildkit determines the vertex digest. Keep this
	// in sync with Buildkit until a better way to do this arrives. It hasn't
	// changed in 5 years, surely it won't soon, right?
	id := ref
	if platform := opt.Platform; platform == nil {
		id += platforms.Format(platforms.DefaultSpec())
	} else {
		id += platforms.Format(*platform)
	}

	rec.Join(digest.FromString(id))

	return g.llbBridge.ResolveImageConfig(ctx, ref, opt)
}

// Solve records the vertexes of the definition and frontend inputs as members
// of the current progress group, and calls the inner Solve.
func (g recordingGateway) Solve(ctx context.Context, req frontend.SolveRequest, sessionID string) (*frontend.Result, error) {
	rec := progrock.RecorderFromContext(ctx)

	if req.Definition != nil {
		core.RecordVertexes(rec, req.Definition)
	}

	for _, input := range req.FrontendInputs {
		if input == nil {
			// TODO(vito): we currently pass a nil def to Dockerfile inputs, should
			// probably change that to llb.Scratch
			continue
		}

		core.RecordVertexes(rec, input)
	}

	return g.llbBridge.Solve(ctx, req, sessionID)
}

func (g recordingGateway) Warn(ctx context.Context, dgst digest.Digest, msg string, opts frontend.WarnOpts) error {
	return g.llbBridge.Warn(ctx, dgst, msg, opts)
}

type progrockLogrusWriter struct{}

func (w progrockLogrusWriter) WriteStatus(ev *progrock.StatusUpdate) error {
	l := bklog.G(context.TODO())
	for _, vtx := range ev.Vertexes {
		l = l.WithField("vertex-"+vtx.Id, vtx)
	}
	for _, task := range ev.Tasks {
		l = l.WithField("task-"+task.Vertex, task)
	}
	for _, log := range ev.Logs {
		l = l.WithField("log-"+log.Vertex, log)
	}
	l.Trace()
	return nil
}

func (w progrockLogrusWriter) Close() error {
	return nil
}

func progrockForwarder(sockPath string, w progrock.Writer) (progrock.Writer, func() error, error) {
	if err := os.MkdirAll(filepath.Dir(sockPath), 0700); err != nil {
		return nil, nil, err
	}
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, nil, err
	}

	progW, err := progrock.ServeRPC(l, w)
	if err != nil {
		return nil, nil, err
	}

	return progW, l.Close, nil
}
