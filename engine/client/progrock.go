package client

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	bkclient "github.com/moby/buildkit/client"
	"github.com/vito/progrock"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type progrockFileWriter struct {
	f   *os.File
	enc *json.Encoder
}

func newProgrockFileWriter(filePath string) (progrock.Writer, error) {
	f, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}

	enc := json.NewEncoder(f)

	return progrockFileWriter{
		f:   f,
		enc: enc,
	}, nil
}

func (w progrockFileWriter) WriteStatus(ev *progrock.StatusUpdate) error {
	return w.enc.Encode(ev)
}

func (w progrockFileWriter) Close() error {
	return w.f.Close()
}

func bk2progrock(event *bkclient.SolveStatus) *progrock.StatusUpdate {
	var status progrock.StatusUpdate
	for _, v := range event.Vertexes {
		vtx := &progrock.Vertex{
			Id:     v.Digest.String(),
			Name:   v.Name,
			Cached: v.Cached,
		}
		if strings.HasPrefix(v.Name, "[internal] ") {
			vtx.Internal = true
			vtx.Name = strings.TrimPrefix(v.Name, "[internal] ")
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
