package main

import (
	"os"
	"strings"

	"github.com/moby/buildkit/identity"
	"github.com/sirupsen/logrus"
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

// some logs from buildkit/containerd libs are not useful even at debug level,
// this hook ignores them
type noiseReductionHook struct {
	ignoreLogger *logrus.Logger
}

var _ logrus.Hook = (*noiseReductionHook)(nil)

func (h *noiseReductionHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

var ignoredMessages = map[string]struct{}{
	"fetch":                        {},
	"fetch response received":      {},
	"resolving":                    {},
	"do request":                   {},
	"resolved":                     {},
	"push":                         {},
	"checking and pushing to":      {},
	"response completed":           {},
	"authorized request":           {},
	"serving grpc connection":      {},
	"diff applied":                 {},
	"using pigz for decompression": {},
}

var ignoredMessagePrefixes = []string{
	"returning network namespace",
	"releasing cni network namespace",
	"creating new network namespace",
	"finished creating network namespace",
	"diffcopy took",
	"Using single walk diff for",
	"reusing ref for",
	"not reusing ref",
}

func (h *noiseReductionHook) Fire(entry *logrus.Entry) error {
	var ignore bool
	if _, ok := ignoredMessages[entry.Message]; ok {
		ignore = true
	} else {
		for _, prefix := range ignoredMessagePrefixes {
			if strings.HasPrefix(entry.Message, prefix) {
				ignore = true
				break
			}
		}
	}
	if ignore {
		entry.Logger = h.ignoreLogger
	}
	return nil
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
