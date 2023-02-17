package main

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	bkclient "github.com/moby/buildkit/client"
)

type Vertex struct {
	v *bkclient.Vertex
}

func (w Vertex) ID() string {
	return w.v.Digest.String()
}

func (w Vertex) Name() string {
	var custom pipeline.CustomName
	if json.Unmarshal([]byte(w.v.Name), &custom) == nil {
		return custom.Name
	}
	return w.v.Name
}

func (w Vertex) Pipeline() pipeline.Path {
	var custom pipeline.CustomName
	if json.Unmarshal([]byte(w.v.Name), &custom) == nil {
		if len(custom.Pipeline) > 0 {
			return custom.Pipeline
		}
	}

	pg := w.v.ProgressGroup.GetId()
	if pg != "" {
		var pipeline pipeline.Path
		if json.Unmarshal([]byte(pg), &pipeline) == nil {
			return pipeline
		}
	}
	return pipeline.Path{}
}

func (w Vertex) Internal() bool {
	var custom pipeline.CustomName
	if json.Unmarshal([]byte(w.v.Name), &custom) == nil {
		if custom.Internal {
			return true
		}
	}
	return false
}

func (w Vertex) Started() time.Time {
	if w.v.Started == nil {
		return time.Time{}
	}
	return *w.v.Started
}

func (w Vertex) Completed() time.Time {
	if w.v.Completed == nil {
		return time.Time{}
	}
	return *w.v.Completed
}

func (w Vertex) Duration() time.Duration {
	return w.Completed().Sub(w.Started())
}

func (w Vertex) Cached() bool {
	return w.v.Cached
}

func (w Vertex) Error() error {
	if w.v.Error == "" {
		return nil
	}
	return errors.New(w.v.Error)
}

func (w Vertex) Inputs() []string {
	inputs := make([]string, 0, len(w.v.Inputs))
	for _, i := range w.v.Inputs {
		inputs = append(inputs, i.String())
	}
	return inputs
}

type VertexList []Vertex

func (l VertexList) Started() time.Time {
	var first time.Time
	for _, v := range l {
		if first.IsZero() || v.Started().Before(first) {
			first = v.Started()
		}
	}
	return first
}

func (l VertexList) Completed() time.Time {
	var last time.Time
	for _, v := range l {
		if last.IsZero() || v.Completed().After(last) {
			last = v.Completed()
		}
	}
	return last
}

func (l VertexList) Cached() bool {
	for _, v := range l {
		if !v.Cached() {
			return false
		}
	}

	// Return true if there is more than one vertex and they're all cached
	return len(l) > 0
}

func (l VertexList) Duration() time.Duration {
	return l.Completed().Sub(l.Started())
}

func (l VertexList) Error() error {
	for _, v := range l {
		if err := v.Error(); err != nil {
			return err
		}
	}
	return nil
}

func (l VertexList) ByPipeline() map[string]VertexList {
	breakdown := map[string]VertexList{}
	for _, v := range l {
		pipeline := v.Pipeline()
		if len(pipeline) == 0 {
			continue
		}
		// FIXME: events should indicate if this is a "built-in" pipeline
		name := pipeline.Name()
		if strings.HasPrefix(name, "from ") ||
			strings.HasPrefix(name, "host.directory") ||
			name == "docker build" {
			continue
		}

		breakdown[pipeline.String()] = append(breakdown[pipeline.String()], v)
	}

	return breakdown
}
