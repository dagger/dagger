package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dagger/dagger/cmd/otel-collector/loki"
)

type Event struct {
	Type     string            `json:"type"`
	Name     string            `json:"name"`
	Duration int64             `json:"duration"`
	Cached   bool              `json:"cached"`
	Error    string            `json:"error,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

const (
	EventTypeRun      = "run"
	EventTypePipeline = "pipeline"
	EventTypeOp       = "op"
)

func logSummary(name string, vertices VertexList, labels map[string]string) error {
	client := loki.New(
		env("GRAFANA_CLOUD_USER_ID"),
		env("GRAFANA_CLOUD_API_KEY"),
		env("GRAFANA_CLOUD_URL"),
	)
	defer client.Flush()

	// Send overall run event
	err := pushEvent(client,
		Event{
			Type:     EventTypeRun,
			Name:     name,
			Duration: vertices.Duration().Microseconds(),
			Cached:   vertices.Cached(),
			Error:    errorString(vertices.Error()),
			Labels:   labels,
		}, vertices.Started())
	if err != nil {
		return err
	}

	// Send pipelines
	for pipeline, vertices := range vertices.ByPipeline() {
		err := pushEvent(client,
			Event{
				Type:     EventTypePipeline,
				Name:     pipeline,
				Duration: vertices.Duration().Microseconds(),
				Cached:   vertices.Cached(),
				Error:    errorString(vertices.Error()),
				Labels:   labels,
			},
			vertices.Started())
		if err != nil {
			return err
		}
	}

	// Send individual vertices
	for _, vertex := range vertices {
		err := pushEvent(client,
			Event{
				Type:     EventTypeOp,
				Name:     vertex.Name(),
				Duration: vertex.Duration().Microseconds(),
				Cached:   vertex.Cached(),
				Error:    errorString(vertex.Error()),
				Labels:   labels,
			}, vertex.Started())
		if err != nil {
			return err
		}
	}

	return nil
}

func pushEvent(client *loki.Client, event Event, ts time.Time) error {
	marshalled, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return client.PushLogLineWithTimestamp(
		string(marshalled),
		ts,
		map[string]string{
			"user":    os.Getenv("USER"),
			"version": "2023-01-18.1851",
			"type":    event.Type,
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
