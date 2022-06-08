package event

type ActionState string

const (
	ActionStateRunning   ActionState = "running"
	ActionStateSkipped   ActionState = "skipped"
	ActionStateCompleted ActionState = "completed"
	ActionStateFailed    ActionState = "failed"
	ActionStateCancelled ActionState = "cancelled"
)

type ActionTransitioned struct {
	Name  string      `json:"name"`
	State ActionState `json:"state"`
	Error string      `json:"error,omitempty"`
}

func (a ActionTransitioned) EventName() string {
	return "action.transitioned"
}

func (a ActionTransitioned) EventVersion() string {
	return eventVersion
}

func (a ActionTransitioned) Validate() error {
	switch {
	case a.Name == "":
		return errEvent("Name", "cannot be empty")
	case a.State == "":
		return errEvent("State", "cannot be empty")
	}
	return nil
}

type ActionLogged struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func (a ActionLogged) EventName() string {
	return "action.logged"
}

func (a ActionLogged) EventVersion() string {
	return eventVersion
}

func (a ActionLogged) Validate() error {
	if a.Name == "" {
		return errEvent("Name", "cannot be empty")
	}
	return nil
}
