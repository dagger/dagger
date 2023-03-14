package telemetry

import (
	"time"

	"github.com/dagger/dagger/core/pipeline"
)

const eventVersion = "2023-02-28.01"

type Event struct {
	Version   string    `json:"v"`
	Timestamp time.Time `json:"ts"`

	RunID string `json:"run_id"`

	Type    EventType `json:"type"`
	Payload Payload   `json:"payload"`
}

type EventType string

type Payload interface {
	Type() EventType
}

var _ Payload = OpPayload{}

type OpPayload struct {
	OpID     string        `json:"op_id"`
	OpName   string        `json:"op_name"`
	Pipeline pipeline.Path `json:"pipeline"`
	Internal bool          `json:"internal"`
	Inputs   []string      `json:"inputs"`

	Started   *time.Time `json:"started"`
	Completed *time.Time `json:"completed"`
	Cached    bool       `json:"cached"`
	Error     string     `json:"error"`
}

func (OpPayload) Type() EventType { return EventType("op") }

var _ Payload = LogPayload{}

type LogPayload struct {
	OpID   string `json:"op_id"`
	Data   string `json:"data"`
	Stream int    `json:"stream"`
}

func (LogPayload) Type() EventType { return EventType("log") }
