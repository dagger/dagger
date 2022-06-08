package event

type Logger struct {
	Message string `json:"message"`
	Level   string `json:"level"`

	Fields map[string]interface{} `json:"fields"`
}

func (e Logger) EventName() string {
	return "log.emitted"
}

func (e Logger) EventVersion() string {
	return eventVersion
}

func (e Logger) Validate() error {
	switch {
	case e.Level == "":
		return errEvent("Level", "cannot be empty")
	case e.Fields == nil:
		return errEvent("Fields", "cannot be empty")
	}

	return nil
}
