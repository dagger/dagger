package event

type ActionState string

const (
	ActionStateRunning   ActionState = "running"
	ActionStateSkipped   ActionState = "skipped"
	ActionStateCompleted ActionState = "completed"
	ActionStateFailed    ActionState = "failed"
	ActionStateCancelled ActionState = "cancelled"
)

// ActionUpdated signals a completed action.
type ActionUpdated struct {
	Name  string      `json:"name"`
	State ActionState `json:"state"`
	Error string      `json:"error,omitempty"`
}

func (a ActionUpdated) EventName() string {
	return "action.updated"
}

func (a ActionUpdated) EventVersion() string {
	return eventVersion
}

func (a ActionUpdated) Validate() error {
	switch {
	case a.Name == "":
		return errEvent("Name", "cannot be empty")
	case a.State == "":
		return errEvent("State", "cannot be empty")
	}
	return nil
}
