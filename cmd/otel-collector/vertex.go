package main

import (
	"errors"
	"strings"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/telemetry"
)

type Vertex struct {
	v *telemetry.PipelinedVertex
}

func (w Vertex) ID() string {
	return w.v.Id
}

func (w Vertex) Name() string {
	return w.v.Name
}

func (w Vertex) Pipeline() pipeline.Path {
	if len(w.v.Pipelines) == 0 {
		return pipeline.Path{}
	}
	return w.v.Pipelines[0]
}

func (w Vertex) Internal() bool {
	return w.v.Internal
}

func (w Vertex) Started() time.Time {
	if w.v.Started == nil {
		return time.Time{}
	}
	return w.v.Started.AsTime()
}

func (w Vertex) Completed() time.Time {
	if w.v.Completed == nil {
		return time.Time{}
	}
	return w.v.Completed.AsTime()
}

func (w Vertex) Duration() time.Duration {
	return w.Completed().Sub(w.Started())
}

func (w Vertex) Cached() bool {
	return w.v.Cached
}

func (w Vertex) Error() error {
	if w.v.Error == nil {
		return nil
	}
	return errors.New(*w.v.Error)
}

func (w Vertex) Inputs() []string {
	return w.v.Inputs
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
