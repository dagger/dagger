package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dagger/dagger/cmd/otel-collector/loki"
)

type Event struct {
	Name     string            `json:"name"`
	Duration int64             `json:"duration"`
	Error    string            `json:"error,omitempty"`
	Tags     map[string]string `json:"tag,omitempty"`
	TraceID  string            `json:"trace_id,omitempty"`
}

func (e Event) Errored() bool {
	return e.Error != ""
}

type Label struct {
	Type    string `json:"type"`
	Cached  bool   `json:"cached"`
	Errored bool   `json:"errored"`
}

const (
	TypeRun      = "run"
	TypePipeline = "pipeline"
	TypeOp       = "op"
)

func logSummary(name string, vertices VertexList, tags map[string]string, traceID string) error {
	client := loki.New(
		env("GRAFANA_CLOUD_USER_ID"),
		env("GRAFANA_CLOUD_API_KEY"),
		env("GRAFANA_CLOUD_URL"),
	)
	defer client.Flush()

	runEvent := Event{
		Name:     name,
		Duration: vertices.Duration().Microseconds(),
		Error:    errorString(vertices.Error()),
		Tags:     tags,
		TraceID:  traceID,
	}
	runLabel := Label{
		Type:    TypeRun,
		Cached:  vertices.Cached(),
		Errored: runEvent.Errored(),
	}
	err := pushEvent(client, runEvent, runLabel, vertices.Started())
	if err != nil {
		return err
	}

	for pipeline, vertices := range vertices.ByPipeline() {
		pipelineEvent := Event{
			Name:     pipeline,
			Duration: vertices.Duration().Microseconds(),
			Error:    errorString(vertices.Error()),
			Tags:     tags,
			TraceID:  traceID,
		}
		pipelineLabel := Label{
			Type:    TypePipeline,
			Cached:  vertices.Cached(),
			Errored: pipelineEvent.Errored(),
		}
		err := pushEvent(client, pipelineEvent, pipelineLabel, vertices.Started())
		if err != nil {
			return err
		}
	}

	for _, vertex := range vertices {
		opEvent := Event{
			Name:     vertex.Name(),
			Duration: vertex.Duration().Microseconds(),
			Error:    errorString(vertex.Error()),
			Tags:     tags,
			TraceID:  traceID,
		}
		opLabel := Label{
			Type:    TypeOp,
			Cached:  vertex.Cached(),
			Errored: opEvent.Errored(),
		}
		err := pushEvent(client, opEvent, opLabel, vertex.Started())
		if err != nil {
			return err
		}
	}

	return nil
}

func pushEvent(client *loki.Client, event Event, label Label, ts time.Time) error {
	marshalled, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return client.PushLogLineWithTimestamp(
		string(marshalled),
		ts,
		map[string]string{
			"user":    os.Getenv("USER"),
			"version": "2023-01-26.1540",
			"type":    label.Type,
			"cached":  fmt.Sprintf("%t", label.Cached),
			"errored": fmt.Sprintf("%t", label.Errored),
		},
	)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func env(varName string) string {
	env := os.Getenv(varName)
	if env == "" {
		fmt.Fprintf(os.Stderr, "env var %s must be set\n", varName)
		os.Exit(1)
	}
	return env
}
