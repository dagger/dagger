package logger

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/google/uuid"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/telemetrylite"
	"go.dagger.io/dagger/version"
)

type Cloud struct {
	client *telemetrylite.TelemetryLite
	run    string
	engine string
}

func NewCloud(tm *telemetrylite.TelemetryLite) *Cloud {
	engineID, _ := engine.ID()

	return &Cloud{
		client: tm,
		run:    uuid.NewString(),
		engine: engineID,
	}
}

type LogEvent struct {
	Arch           string   `json:"arch"`
	Args           []string `json:"args"`
	Caller         string   `json:"caller"`
	DaggerRevision string   `json:"daggerRevision"`
	DaggerVersion  string   `json:"daggerVersion"`
	Duration       float64  `json:"duration,omitempty"`
	EngineID       string   `json:"engineId"`
	Error          string   `json:"error,omitempty"`
	Level          string   `json:"level"`
	Message        string   `json:"message,omitempty"`
	OS             string   `json:"os"`
	RunID          string   `json:"runId"`
	State          string   `json:"state,omitempty"`
	Task           string   `json:"task,omitempty"`
	Time           string   `json:"time"`
}

func (c *Cloud) Write(p []byte) (int, error) {
	event := LogEvent{
		RunID:          c.run,
		Arch:           runtime.GOARCH,
		OS:             runtime.GOOS,
		DaggerVersion:  version.Version,
		DaggerRevision: version.Revision,
		EngineID:       c.engine,
	}
	if err := json.Unmarshal(p, &event); err != nil {
		return 0, fmt.Errorf("cannot unmarshal event: %s", err)
	}
	fmt.Printf("🐟 EVENT: %#v\n", event)

	jsonData, err := json.Marshal(event)
	fmt.Printf("🐥 JSON: %#v\n", string(jsonData))
	if err != nil {
		return 0, fmt.Errorf("cannot marshal event: %s", err)
	}
	c.client.Push(jsonData)
	return len(p), nil
}
