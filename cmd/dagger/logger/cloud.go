package logger

import (
	"encoding/json"
	"fmt"
	"runtime"

	"go.dagger.io/dagger/telemetry"
	"go.dagger.io/dagger/version"
)

type Cloud struct {
	tm *telemetry.Telemetry
}

func NewCloud(tm *telemetry.Telemetry) *Cloud {
	return &Cloud{
		tm: tm,
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
		RunID:          c.tm.RunID(),
		EngineID:       c.tm.EngineID(),
		Arch:           runtime.GOARCH,
		OS:             runtime.GOOS,
		DaggerVersion:  version.Version,
		DaggerRevision: version.Revision,
	}
	if err := json.Unmarshal(p, &event); err != nil {
		return 0, fmt.Errorf("cannot unmarshal event: %s", err)
	}
	fmt.Printf("🐟 EVENT: %#v\n", event)

	jsonData, err := json.Marshal(event)
	fmt.Printf("🐔 JSON: %#v\n", string(jsonData))
	if err != nil {
		return 0, fmt.Errorf("cannot marshal event: %s", err)
	}
	c.tm.Write(jsonData)
	return len(p), nil
}
