package event

type ActionState string

const (
	ActionStateRunning     ActionState = "running"
	ActionStateSkipped     ActionState = "skipped"
	ActionStateCompleted   ActionState = "completed"
	ActionStateFailed      ActionState = "failed"
	ActionStateCancelled   ActionState = "cancelled"
	ActionStateCacheUpdate ActionState = "cache-update"
)

type ActionCacheStatus string

const (
	ActionCacheStatusNone    ActionCacheStatus = "none"
	ActionCacheStatusPartial ActionCacheStatus = "partial"
	ActionCacheStatusCached  ActionCacheStatus = "cached"
)

type ActionTransitioned struct {
	Name        string            `json:"name"`
	State       ActionState       `json:"state"`
	Error       string            `json:"error,omitempty"`
	CacheStatus ActionCacheStatus `json:"cache_status,omitempty"`
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
