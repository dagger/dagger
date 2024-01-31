package buildkit

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/platforms"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const FocusPrefix = "[focus] "
const InternalPrefix = "[internal] "

type recordingGateway struct {
	llbBridge frontend.FrontendLLBBridge
}

// ResolveImageConfig records the image config resolution vertex as a member of
// the current progress group, and calls the inner ResolveImageConfig.
func (g recordingGateway) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (string, digest.Digest, []byte, error) {
	rec := progrock.FromContext(ctx)

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
	rec := progrock.FromContext(ctx)

	if req.Definition != nil {
		RecordVertexes(rec, req.Definition)
	}

	for _, input := range req.FrontendInputs {
		if input == nil {
			// TODO(vito): we currently pass a nil def to Dockerfile inputs, should
			// probably change that to llb.Scratch
			continue
		}

		RecordVertexes(rec, input)
	}

	return g.llbBridge.Solve(ctx, req, sessionID)
}

func (g recordingGateway) Warn(ctx context.Context, dgst digest.Digest, msg string, opts frontend.WarnOpts) error {
	return g.llbBridge.Warn(ctx, dgst, msg, opts)
}

type ProgrockLogrusWriter struct{}

func (w ProgrockLogrusWriter) WriteStatus(ev *progrock.StatusUpdate) error {
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

func (w ProgrockLogrusWriter) Close() error {
	return nil
}

func ProgrockForwarder(sockPath string, w progrock.Writer) (progrock.Writer, func() error, error) {
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

func RecordVertexes(recorder *progrock.Recorder, def *pb.Definition) {
	dgsts := []digest.Digest{}
	for dgst, meta := range def.Metadata {
		_ = meta
		if meta.ProgressGroup != nil {
			// Regular progress group, i.e. from Dockerfile; record it as a subgroup,
			// with 'weak' annotation so it's distinct from user-configured
			// pipelines.
			recorder.WithGroup(meta.ProgressGroup.Name, progrock.Weak()).Join(dgst)
		} else {
			dgsts = append(dgsts, dgst)
		}
	}

	recorder.Join(dgsts...)
}

func RecordBuildkitStatus(rec *progrock.Recorder, solveCh <-chan *bkclient.SolveStatus) error {
	for ev := range solveCh {
		if err := rec.Record(BK2Progrock(ev)); err != nil {
			return fmt.Errorf("record: %w", err)
		}
	}
	return nil
}

func BK2Progrock(event *bkclient.SolveStatus) *progrock.StatusUpdate {
	var status progrock.StatusUpdate
	for _, v := range event.Vertexes {
		vtx := &progrock.Vertex{
			Id:     v.Digest.String(),
			Name:   v.Name,
			Cached: v.Cached,
		}
		if strings.HasPrefix(v.Name, InternalPrefix) {
			vtx.Internal = true
			vtx.Name = strings.TrimPrefix(v.Name, InternalPrefix)
		}
		if strings.HasPrefix(v.Name, FocusPrefix) {
			vtx.Focused = true
			vtx.Name = strings.TrimPrefix(v.Name, FocusPrefix)
		}
		for _, input := range v.Inputs {
			vtx.Inputs = append(vtx.Inputs, input.String())
		}
		if v.Started != nil {
			vtx.Started = timestamppb.New(*v.Started)
		}
		if v.Completed != nil {
			vtx.Completed = timestamppb.New(*v.Completed)
		}
		if v.Error != "" {
			if strings.HasSuffix(v.Error, context.Canceled.Error()) {
				vtx.Canceled = true
			} else {
				msg := v.Error
				vtx.Error = &msg
			}
		}
		status.Vertexes = append(status.Vertexes, vtx)
	}

	for _, s := range event.Statuses {
		task := &progrock.VertexTask{
			Vertex:  s.Vertex.String(),
			Name:    s.ID, // remap
			Total:   s.Total,
			Current: s.Current,
		}
		if s.Started != nil {
			task.Started = timestamppb.New(*s.Started)
		}
		if s.Completed != nil {
			task.Completed = timestamppb.New(*s.Completed)
		}
		status.Tasks = append(status.Tasks, task)
	}

	for _, s := range event.Logs {
		status.Logs = append(status.Logs, &progrock.VertexLog{
			Vertex:    s.Vertex.String(),
			Stream:    progrock.LogStream(s.Stream),
			Data:      s.Data,
			Timestamp: timestamppb.New(s.Timestamp),
		})
	}

	return &status
}
