package main

import (
	"os"

	"github.com/moby/buildkit/identity"
)

var (
	engineName string
)

func init() {
	var ok bool
	engineName, ok = os.LookupEnv("_EXPERIMENTAL_DAGGER_ENGINE_NAME")
	if !ok {
		// use the hostname
		hostname, err := os.Hostname()
		if err != nil {
			engineName = "rand-" + identity.NewID() // random ID as a fallback
		} else {
			engineName = hostname
		}
	}

	// TODO(vito): send engine logs over OTLP
	// tel = telemetry.New()

	// logrus.AddHook(&cloudHook{})
}

// type cloudHook struct{}

// var _ logrus.Hook = (*cloudHook)(nil)

// func (h *cloudHook) Levels() []logrus.Level {
// 	return logrus.AllLevels
// }

// func (h *cloudHook) Fire(entry *logrus.Entry) error {
// 	payload := &engineLogPayload{
// 		Engine: engineMetadata{
// 			Name: engineName,
// 		},
// 		Message: entry.Message,
// 		Level:   entry.Level.String(),
// 		Fields:  entry.Data,
// 	}

// 	tel.Push(payload, entry.Time)
// 	return nil
// }

// type engineLogPayload struct {
// 	Engine  engineMetadata `json:"engine"`
// 	Message string         `json:"message"`
// 	Level   string         `json:"level"`
// 	// NOTE: fields includes traceID and spanID, can we use that to correlate with clients?
// 	Fields map[string]any `json:"fields"`
// }

// func (engineLogPayload) Type() telemetry.EventType {
// 	return telemetry.EventType("engine_log")
// }

// func (engineLogPayload) Scope() telemetry.EventScope {
// 	return telemetry.EventScopeSystem
// }

// type engineMetadata struct {
// 	Name string `json:"name"`
// }
