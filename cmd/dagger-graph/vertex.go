package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/telemetry"
)

type WrappedVertex struct {
	v *telemetry.PipelinedVertex
}

func (w WrappedVertex) ID() string {
	return w.v.Id
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
	return w.v.Name
}

func (w WrappedVertex) Pipeline() pipeline.Path {
	if len(w.v.Pipelines) == 0 {
		return pipeline.Path{}
	}
	return w.v.Pipelines[0]
}

func (w WrappedVertex) Internal() bool {
	return w.v.Internal
}

func (w WrappedVertex) Inputs() []string {
	return w.v.Inputs
}

func (w WrappedVertex) Started() time.Time {
	if w.v.Started == nil {
		return time.Time{}
	}
	return w.v.Started.AsTime()
}

func (w WrappedVertex) Completed() time.Time {
	if w.v.Completed == nil {
		return time.Time{}
	}
	return w.v.Completed.AsTime()
}

func (w WrappedVertex) Duration() time.Duration {
	return w.Completed().Sub(w.Started())
}

func (w WrappedVertex) Cached() bool {
	return w.v.Cached
}
