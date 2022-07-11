package event

// RunStarted represents the start of a run
type RunStarted struct {
	Action string   `json:"action"`
	Args   []string `json:"args"`
	Plan   string   `json:"plan"`
}

func (e RunStarted) EventName() string {
	return "run.started"
}

func (e RunStarted) EventVersion() string {
	return eventVersion
}

func (e RunStarted) Validate() error {
	return nil
}

type RunCompletedState string

const (
	RunCompletedStateSuccess RunCompletedState = "success"
	RunCompletedStateFailed  RunCompletedState = "failed"
)

type RunError struct {
	Message   string            `json:"message,omitempty"`
	PlanFiles map[string]string `json:"plan_files,omitempty"`
}

// RunCompleted represents the completion of a run
type RunCompleted struct {
	State RunCompletedState `json:"state"`
	// Used until Dagger 0.2.21 should be replaced by `Err` after all clients
	// migrate. We're doing this here to prevent versioning the API and the
	// go package for now
	Error string `json:"error,omitempty"`

	// Err should eventually replace `Error`
	Err     *RunError         `json:"err,omitempty"`
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
