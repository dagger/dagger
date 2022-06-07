package event

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"go.dagger.io/dagger/version"
)

const eventVersion = "2022-05-25.01"

type Event struct {
	// Name is the type of the events
	// Format is as such: `<object>.<action>`, e.g. `action.started`
	Name string `json:"name"`

	Version   string `json:"v"`
	Timestamp int64  `json:"ts"`

	Data interface{} `json:"data,omitempty"`

	Engine engineProperties `json:"engine"`
	Run    runProperties    `json:"run,omitempty"`
}

type engineProperties struct {
	ID string `json:"id"`

	Version  string `json:"version"`
	Revision string `json:"revision"`

	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type runProperties struct {
	ID string `json:"id,omitempty"`
}

func (e engineProperties) Validate() error {
	switch {
	case e.Version == "":
		return errEvent("Version", "cannot be empty")
	case e.OS == "":
		return errEvent("OS", "cannot be empty")
	case e.Arch == "":
		return errEvent("Arch", "cannot be empty")
	}
	return nil
}

type Properties interface {
	EventName() string
	EventVersion() string
	Validate() error
}

func (e *Event) Validate() error {
	switch {
	case e.Name == "":
		return errEvent("Name", "cannot be empty")
	case !strings.Contains(e.Name, "."):
		return errEvent("Name", "must contain '.'")
	case e.Version == "":
		return errEvent("Version", "cannot be empty")
	case e.Timestamp == 0:
		return errEvent("Timestamp", "cannot be empty")
	}

	if err := e.Engine.Validate(); err != nil {
		return err
	}

	if err := e.Data.(Properties).Validate(); err != nil {
		return err
	}

	return nil
}

func New(props Properties) *Event {
	return &Event{
		Name: props.EventName(),

		Version:   props.EventVersion(),
		Timestamp: time.Now().UTC().UnixNano(),

		Data: props,

		Engine: engineProperties{
			Version:  version.Version,
			Revision: version.Revision,

			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
	}
}

func errEvent(property string, issue string) error {
	return fmt.Errorf("event: %s %s", property, issue)
}
