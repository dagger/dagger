package event

type ActionState string

const (
	ActionStateRunning   ActionState = "running"
	ActionStateSkipped   ActionState = "skipped"
	ActionStateCompleted ActionState = "completed"
	ActionStateFailed    ActionState = "failed"
	ActionStateCancelled ActionState = "cancelled"
)

type ActionTransition struct {
	Name  string      `json:"name"`
	State ActionState `json:"state"`
	Error string      `json:"error,omitempty"`
}

func (a ActionTransition) EventName() string {
	return "action.transition"
}

func (a ActionTransition) EventVersion() string {
	return eventVersion
}

func (a ActionTransition) Validate() error {
	switch {
	case a.Name == "":
		return errEvent("Name", "cannot be empty")
	case a.State == "":
		return errEvent("State", "cannot be empty")
	}
	return nil
}
