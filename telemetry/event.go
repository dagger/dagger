package telemetry

import (
	"time"

	"github.com/dagger/dagger/dagql/idproto"
)

const eventVersion = "2023-02-28.01"

// /events/<run-id>

type Event struct {
	Version   string    `json:"v"`
	Timestamp time.Time `json:"ts"`

	// TODO: maybe get rid of this? tied to how the old schema materialized
	RunID string `json:"run_id,omitempty"`

	Type    EventType `json:"type"`
	Payload Payload   `json:"payload"`
}

type EventType string

type EventScope string

const (
	EventScopeSystem = EventScope("system")
	EventScopeRun    = EventScope("run")
)

const (
	EventTypeSpan = EventType("span")
	EventTypeCall = EventType("call")
	EventTypeLog  = EventType("log")
	EventTypeRun  = EventType("run")
)

type Payload interface {
	Type() EventType
	Scope() EventScope
}

var _ Payload = LogPayload{}

type LogPayload struct {
	SpanID string `json:"span_id"`
	Data   string `json:"data"`
	Stream int    `json:"stream"`
}

func (LogPayload) Type() EventType   { return EventTypeLog }
func (LogPayload) Scope() EventScope { return EventScopeRun }

var _ Payload = SpanPayload{}

type SpanPayload struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`

	Name     string   `json:"name"`
	Internal bool     `json:"internal,omitempty"`
	Inputs   []string `json:"inputs,omitempty"`

	// Comes from Vertex.Output
	ResultCallID string `json:"result_call_id,omitempty"`
	// TODO add this when we want to store arbitrary query results
	// ResultValue any `json:"result_value,omitempty"`

	Started   *time.Time `json:"started,omitempty"`
	Completed *time.Time `json:"completed,omitempty"`
	Cached    bool       `json:"cached,omitempty"`
	Error     string     `json:"error,omitempty"`
}

func (SpanPayload) Type() EventType   { return EventTypeSpan }
func (SpanPayload) Scope() EventScope { return EventScopeRun }

var _ Payload = CallPayload{}

type CallPayload struct {
	ID string `json:"id"`

	ProtobufPayload []byte        `json:"protobuf_payload"`
	ReturnType      *idproto.Type `json:"return_type"`

	ReceiverID string    `json:"receiver_id,omitempty"`
	Function   string    `json:"function"`
	Args       []CallArg `json:"args,omitempty"`

	ModuleName          string `json:"module_name,omitempty"`
	ModuleRef           string `json:"module_ref,omitempty"`
	ModuleConstructorID string `json:"module_constructor_id,omitempty"`

	Tainted bool  `json:"tainted,omitempty"`
	Nth     int64 `json:"nth,omitempty"`
}

// CallLiteral is a different representation of idproto.Literal where IDs are
// referred to by their digest, instead of included wholesale.
type CallLiteral struct {
	ID     *string        `json:"id,omitempty"`
	Null   *bool          `json:"null,omitempty"`
	Bool   *bool          `json:"bool,omitempty"`
	Int    *int64         `json:"int,omitempty"`
	Float  *float64       `json:"float,omitempty"`
	Enum   *string        `json:"enum,omitempty"`
	String *string        `json:"string,omitempty"`
	List   *[]CallLiteral `json:"list,omitempty"`
	Object *[]CallArg     `json:"object,omitempty"`
}

type CallArg struct {
	Name  string      `json:"name"`
	Value CallLiteral `json:"value"`
}

func (CallPayload) Type() EventType   { return EventTypeCall }
func (CallPayload) Scope() EventScope { return EventScopeRun }

var _ Payload = RunPayload{}

type RunPayload struct {
	Labels      map[string]string `json:"labels,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
	Error       string            `json:"error,omitempty"`
}

func (RunPayload) Type() EventType   { return EventTypeRun }
func (RunPayload) Scope() EventScope { return EventScopeRun }

func (pl RunPayload) Clone() *RunPayload {
	labels := make(map[string]string, len(pl.Labels))
	for k, v := range pl.Labels {
		labels[k] = v
	}
	return &RunPayload{
		Labels:      labels,
		StartedAt:   pl.StartedAt,
		CompletedAt: pl.CompletedAt,
		Error:       pl.Error,
	}
}
