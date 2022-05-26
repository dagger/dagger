package event

// LogEmitted represents a log message
type LogEmitted struct {
	Message string `json:"message"`
	Level   string `json:"level"`

	Fields map[string]interface{} `json:"fields"`
}

func (e LogEmitted) EventName() string {
	return "log.emitted"
}

func (e LogEmitted) EventVersion() string {
	return eventVersion
}

func (e LogEmitted) Validate() error {
	switch {
	case e.Message == "":
		return ErrMalformedEvent
	case e.Level == "":
		return ErrMalformedEvent
	case e.Fields == nil:
		return ErrMalformedEvent
	}
	return nil
}
