package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/google/uuid"
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

	t := NewTelemetry()

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

func processJournal(t *Telemetry, entries chan *JournalEntry) error {
	runID := uuid.NewString()

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

			ev := Event{
				Version:   eventVersion,
				Timestamp: entry.TS,
				RunID:     runID,

				OpID:     id,
				OpName:   custom.Name,
				Internal: custom.Internal,
				Pipeline: custom.Pipeline,

				Started:   v.Started,
				Completed: v.Completed,
				Cached:    v.Cached,
			}

			ev.Inputs = []string{}
			for _, input := range v.Inputs {
				ev.Inputs = append(ev.Inputs, input.String())
			}

			t.Push(&ev)
		}
	}

	return nil
}
