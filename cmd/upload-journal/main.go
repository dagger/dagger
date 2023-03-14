package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/telemetry"
)

func main() {
	var followFlag bool
	flag.BoolVar(&followFlag, "f", false, "follow")

	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s [-f] <file>\n", os.Args[0])
		os.Exit(1)
	}
	journal := args[0]

	t := telemetry.New()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	stopCh := make(chan struct{})
	go func() {
		defer close(stopCh)
		<-sigCh
	}()

	entries, err := tailJournal(journal, followFlag, stopCh)
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		os.Exit(1)
	}
	err = processJournal(t, entries)
	t.Flush()
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		os.Exit(1)
	}
}

func processJournal(t *telemetry.Telemetry, entries chan *JournalEntry) error {
	for entry := range entries {
		for _, v := range entry.Event.Vertexes {
			id := v.Digest.String()

			var custom pipeline.CustomName
			if json.Unmarshal([]byte(v.Name), &custom) != nil {
				custom.Name = v.Name
				if pg := v.ProgressGroup.GetId(); pg != "" {
					if err := json.Unmarshal([]byte(pg), &custom.Pipeline); err != nil {
						return err
					}
				}
			}

			payload := telemetry.OpPayload{
				OpID:     id,
				OpName:   custom.Name,
				Internal: custom.Internal,
				Pipeline: custom.Pipeline,

				Started:   v.Started,
				Completed: v.Completed,
				Cached:    v.Cached,
				Error:     v.Error,
			}

			payload.Inputs = []string{}
			for _, input := range v.Inputs {
				payload.Inputs = append(payload.Inputs, input.String())
			}

			t.Push(payload, entry.TS)
		}

		for _, l := range entry.Event.Logs {
			t.Push(telemetry.LogPayload{
				OpID:   l.Vertex.String(),
				Data:   string(l.Data),
				Stream: l.Stream,
			}, l.Timestamp)
		}
	}

	return nil
}
