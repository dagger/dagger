package event

import (
	"errors"
	"runtime"
	"strings"
	"time"

	"go.dagger.io/dagger/version"
)

var (
	ErrMalformedEvent = errors.New("malformed event properties")
)

const eventVersion = "2022-05-25.01"

type Event struct {
	// Name is the type of the events
	// Format is as such: `<object>.<action>`, e.g. `action.started`
	Name string `json:"name"`

	Version   string `json:"v"`
	Timestamp int64  `json:"ts"`

	Data Properties `json:"data,omitempty"`

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
	// FIXME: not implemented
	// case e.ID == "":
	// 	return ErrMalformedEvent
	case e.Version == "":
		return ErrMalformedEvent
	case e.OS == "":
		return ErrMalformedEvent
	case e.Arch == "":
		return ErrMalformedEvent
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
		return ErrMalformedEvent
	case !strings.Contains(e.Name, "."):
		return ErrMalformedEvent
	case e.Version == "":
		return ErrMalformedEvent
	case e.Timestamp == 0:
		return ErrMalformedEvent
	}

	if err := e.Engine.Validate(); err != nil {
		return err
	}

	if err := e.Data.Validate(); err != nil {
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
