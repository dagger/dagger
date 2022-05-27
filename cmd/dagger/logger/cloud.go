package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/google/uuid"
	"go.dagger.io/dagger/api"
)

type Cloud struct {
	client *api.Client
	url    string
	run    string
}

// TODO: track at the beginning of the run, in the first action
// - engineID
// - DaggerVersion
// - DaggerRevision
// - OS
// - Arch
func NewCloud() *Cloud {
	return &Cloud{
		client: api.New(),
		url:    eventsURL(),
		run:    uuid.NewString(),
	}
}

// TODO: reconcile with analytics/analytics.go
// TODO: follow-up https://github.com/dagger/dagger/pull/2515
type LogEvent struct {
	// TODO: track the starting event instead of tracking all args
	Args     []string `json:"args"`
	Caller   string   `json:"caller"`
	Duration float64  `json:"duration,omitempty"`
	Error    string   `json:"error,omitempty"`
	Level    string   `json:"level"`
	Message  string   `json:"message,omitempty"`
	RunID    string   `json:"runId"`
	State    string   `json:"state,omitempty"`
	Task     string   `json:"task,omitempty"`
	Time     string   `json:"time"`
}

func (c *Cloud) Write(p []byte) (int, error) {
	event := LogEvent{
		RunID: c.run,
	}
	if err := json.Unmarshal(p, &event); err != nil {
		return 0, fmt.Errorf("cannot unmarshal event: %s", err)
	}
	jsonData, _ := json.Marshal(event)
	reqBody := bytes.NewBuffer(jsonData)
	fmt.Printf("%s", reqBody)
	req, err := http.NewRequest(http.MethodPost, c.url, reqBody)
	if err == nil {
		if resp, err := c.client.Do(req.Context(), req); err == nil {
			defer resp.Body.Close()
		}
	}
	return len(p), nil
}

func eventsURL() string {
	url := os.Getenv("DAGGER_CLOUD_EVENTS_URL")
	if url == "" {
		url = "http://localhost:8020/events"
	}
	return url
}
