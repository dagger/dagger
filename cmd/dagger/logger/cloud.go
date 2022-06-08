package logger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog"
	"go.dagger.io/dagger/telemetry"
	"go.dagger.io/dagger/telemetry/event"
)

type Cloud struct {
	tm *telemetry.Telemetry
}

func NewCloud(tm *telemetry.Telemetry) *Cloud {
	return &Cloud{
		tm: tm,
	}
}

func (c *Cloud) Write(p []byte) (int, error) {
	e := map[string]interface{}{}
	if err := json.Unmarshal(p, &e); err != nil {
		return 0, fmt.Errorf("cannot unmarshal event: %s", err)
	}
	message, _ := e[zerolog.MessageFieldName].(string)
	delete(e, zerolog.MessageFieldName)
	level := e[zerolog.LevelFieldName].(string)
	delete(e, zerolog.LevelFieldName)

	c.tm.Push(context.Background(), event.LogEmitted{
		Message: message,
		Level:   level,
		Fields:  e,
	})

	// if there's a fatal event, manually call flush since otherwise the
	// program will exit immediately
	if level == zerolog.LevelFatalValue {
		c.tm.Flush()
	}

	return len(p), nil
}
