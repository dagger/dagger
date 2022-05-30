package event

// ActionPending signals a discovered action, not yet executed.
type ActionPending struct {
	Name string `json:"name"`
}

func (a ActionPending) EventName() string {
	return "action.pending"
}

func (a ActionPending) EventVersion() string {
	return eventVersion
}

func (a ActionPending) Validate() error {
	if a.Name == "" {
		return ErrMalformedEvent
	}
	return nil
}

// ActionStarted signals the start of an action
type ActionStarted struct {
	Name string `json:"name"`
}

func (a ActionStarted) EventName() string {
	return "action.started"
}

func (a ActionStarted) EventVersion() string {
	return eventVersion
}

func (a ActionStarted) Validate() error {
	if a.Name == "" {
		return ErrMalformedEvent
	}
	return nil
}

// ActionSkipped signals a skipped action.
type ActionSkipped struct {
	Name string `json:"name"`
}

func (a ActionSkipped) EventName() string {
	return "action.skipped"
}

func (a ActionSkipped) EventVersion() string {
	return eventVersion
}

func (a ActionSkipped) Validate() error {
	if a.Name == "" {
		return ErrMalformedEvent
	}
	return nil
}

// ActionCancelled signals a cancelled action.
type ActionCancelled struct {
	Name string `json:"name"`
}

func (a ActionCancelled) EventName() string {
	return "action.cancelled"
}

func (a ActionCancelled) EventVersion() string {
	return eventVersion
}

func (a ActionCancelled) Validate() error {
	if a.Name == "" {
		return ErrMalformedEvent
	}
	return nil
}

// ActionFailed signals a failed action.
type ActionFailed struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

func (a ActionFailed) EventName() string {
	return "action.failed"
}

func (a ActionFailed) EventVersion() string {
	return eventVersion
}

func (a ActionFailed) Validate() error {
	if a.Name == "" {
		return ErrMalformedEvent
	}
	if a.Error == "" {
		return ErrMalformedEvent
	}
	return nil
}

// ActionCompleted signals a completed action.
type ActionCompleted struct {
	Name string `json:"name"`
}

func (a ActionCompleted) EventName() string {
	return "action.completed"
}

func (a ActionCompleted) EventVersion() string {
	return eventVersion
}

func (a ActionCompleted) Validate() error {
	if a.Name == "" {
		return ErrMalformedEvent
	}
	return nil
}
