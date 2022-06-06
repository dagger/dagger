package event

// RunStarted represents the start of a run
type RunStarted struct {
	Action string   `json:"action"`
	Args   []string `json:"args"`
}

func (e RunStarted) EventName() string {
	return "run.started"
}

func (e RunStarted) EventVersion() string {
	return eventVersion
}

func (e RunStarted) Validate() error {
	if e.Action == "" {
		return errEvent("Action", "cannot be empty")
	}
	return nil
}

type RunCompletedState string

const (
	RunCompletedStateSuccess RunCompletedState = "success"
	RunCompletedStateFailed  RunCompletedState = "failed"
)

// RunCompleted represents the completion of a run
type RunCompleted struct {
	State   RunCompletedState `json:"state"`
	Error   string            `json:"error,omitempty"`
	Outputs map[string]string `json:"outputs,omitempty"`
}

func (e RunCompleted) EventName() string {
	return "run.completed"
}

func (e RunCompleted) EventVersion() string {
	return eventVersion
}

func (e RunCompleted) Validate() error {
	if e.State != RunCompletedStateSuccess && e.State != RunCompletedStateFailed {
		return errEvent("State", "must have either Succeeded or Failed")
	}
	return nil
}
