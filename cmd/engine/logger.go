package main

import (
	"strings"

	"github.com/sirupsen/logrus"
)

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
	"diff applied":                 {},
	"using pigz for decompression": {},
}

var ignoredMessagePrefixes = []string{
	"Using single walk diff for",
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
