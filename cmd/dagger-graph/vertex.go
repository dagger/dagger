package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dagger/dagger/core"
	bkclient "github.com/moby/buildkit/client"
)

type WrappedVertex struct {
	v *bkclient.Vertex
}

func (w WrappedVertex) ID() string {
	return w.v.Digest.String()
}

func (w WrappedVertex) FullName() string {
	path := []string{}
	for _, p := range w.Pipeline() {
		path = append(path, p.Name)
	}
	path = append(path, fmt.Sprintf("%q", w.ID()))
	return strings.Join(path, ".")
}

func (w WrappedVertex) Name() string {
	var custom core.CustomName
	if json.Unmarshal([]byte(w.v.Name), &custom) == nil {
		return custom.Name
	}
	return w.v.Name
}

func (w WrappedVertex) Pipeline() core.PipelinePath {
	var custom core.CustomName
	if json.Unmarshal([]byte(w.v.Name), &custom) == nil {
		if len(custom.Pipeline) > 0 {
			return custom.Pipeline
		}
	}

	pg := w.v.ProgressGroup.GetId()
	if pg != "" {
		var pipeline core.PipelinePath
		if json.Unmarshal([]byte(pg), &pipeline) == nil {
			return pipeline
		}
	}
	return core.PipelinePath{}
}

func (w WrappedVertex) Internal() bool {
	var custom core.CustomName
	if json.Unmarshal([]byte(w.v.Name), &custom) == nil {
		if custom.Internal {
			return true
		}
	}
	return false
}

func (w WrappedVertex) Inputs() []string {
	inputs := make([]string, 0, len(w.v.Inputs))
	for _, i := range w.v.Inputs {
		inputs = append(inputs, i.String())
	}
	return inputs
}

func (w WrappedVertex) Started() time.Time {
	if w.v.Started == nil {
		return time.Time{}
	}
	return *w.v.Started
}

func (w WrappedVertex) Completed() time.Time {
	if w.v.Completed == nil {
		return time.Time{}
	}
	return *w.v.Completed
}

func (w WrappedVertex) Duration() time.Duration {
	return w.Completed().Sub(w.Started())
}

func (w WrappedVertex) Cached() bool {
	return w.v.Cached
}
