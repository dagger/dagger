package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/rs/zerolog"
	"go.dagger.io/dagger/telemetry"
	"go.dagger.io/dagger/telemetry/event"
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
	Args         []string          `json:"args,omitempty"`
	Caller       string            `json:"caller"`
	Duration     float64           `json:"duration,omitempty"`
	Environment  map[string]string `json:"environment"`
	EngineID     string            `json:"engineId"`
	Error        string            `json:"error,omitempty"`
	Level        string            `json:"level"`
	Message      string            `json:"message,omitempty"`
	RunID        string            `json:"runId"`
	State        string            `json:"state,omitempty"`
	Task         string            `json:"task,omitempty"`
	Time         string            `json:"time"`
	Plan         string            `json:"plan,omitempty"`
	TargetAction string            `json:"targetAction,omitempty"`
	Event        string            `json:"event,omitempty"`
}

func (c *Cloud) Write(p []byte) (int, error) {
	event2 := map[string]interface{}{}
	if err := json.Unmarshal(p, &event2); err != nil {
		return 0, fmt.Errorf("cannot unmarshal event: %s", err)
	}
	message, _ := event2[zerolog.MessageFieldName].(string)
	delete(event2, zerolog.MessageFieldName)
	level := event2[zerolog.LevelFieldName].(string)
	delete(event2, zerolog.LevelFieldName)

	c.tm.Push(context.Background(), event.LogEmitted{
		Message: message,
		Level:   level,
		Fields:  event2,
	})

	// TODO: Remove LogEvent{} below 👇 when the API works with 👆
	event := LogEvent{
		RunID:    c.tm.RunID(),
		EngineID: c.tm.EngineID(),
		Environment: map[string]string{
			"Arch":            runtime.GOARCH,
			"OS":              runtime.GOOS,
			"Dagger version":  version.Version,
			"Dagger revision": version.Revision,
		},
	}
	if err := json.Unmarshal(p, &event); err != nil {
		return 0, fmt.Errorf("cannot unmarshal event: %s", err)
	}

	jsonData, err := json.Marshal(event)
	if err != nil {
		return 0, fmt.Errorf("cannot marshal event: %s", err)
	}
	c.tm.Write(jsonData)

	// if there's a fatal event, manually call flush since otherwise the
	// program will exit immediately
	if event.Level == zerolog.LevelFatalValue {
		c.tm.Flush()
	}

	return len(p), nil
}
