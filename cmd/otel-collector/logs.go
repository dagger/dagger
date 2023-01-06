package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dagger/dagger/cmd/otel-collector/loki"
	bkclient "github.com/moby/buildkit/client"
)

type Event struct {
	Name     string `json:"name"`
	Cached   bool   `json:"cached"`
	Duration int64  `json:"duration"`
}

func logSummary(collector *OtelCollector) {
	client := loki.New(
		env("GRAFANA_CLOUD_USER_ID"),
		env("GRAFANA_CLOUD_API_KEY"),
		env("GRAFANA_CLOUD_URL"),
	)
	defer client.Flush()

	for _, vertex := range collector.Vertices() {
		event := &Event{Name: vertex.Name, Duration: duration(vertex), Cached: vertex.Cached}
		eventJson, err := jsonize(event)
		if err != nil {
			fmt.Printf("VERTEX: %#v\n", vertex)
			continue
		}

		err = client.PushLogLine(string(eventJson), map[string]string{"user": os.Getenv("USER"), "version": "2023-01-06.1550"})
		if err != nil {
			fmt.Println("\nCOULD NOT CONVERT TO JSON")
			fmt.Fprintf(os.Stderr, "%#v", err)
			os.Exit(1)
		}
	}
}

func duration(v *bkclient.Vertex) int64 {
	if v.Cached {
		return 0
	}
	if v.Started == nil {
		return 0
	}
	if v.Completed == nil {
		return 0
	}

	return v.Completed.Sub(*v.Started).Microseconds()
}

func env(varName string) string {
	env := os.Getenv(varName)
	if env == "" {
		fmt.Fprintf(os.Stderr, "env var %s must be set\n", varName)
		os.Exit(1)
	}
	return env
}

func jsonize(event *Event) ([]byte, error) {
	eventJson, err := json.Marshal(event)
	if err != nil {
		return []byte(""), err
	}

	prettyJSON, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		fmt.Println("\n⚠️ COULD NOT CONVERT TO JSON")
		fmt.Printf("EVENT:%s\n", string(prettyJSON))
		return []byte(""), err
	}

	return eventJson, nil
}
