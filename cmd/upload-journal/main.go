package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/telemetry"
	bkclient "github.com/moby/buildkit/client"
	"github.com/nxadm/tail"
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

type JournalEntry struct {
	Event *bkclient.SolveStatus
	TS    time.Time
}

func tailJournal(journal string, follow bool, stopCh chan struct{}) (chan *JournalEntry, error) {
	f, err := tail.TailFile(journal, tail.Config{Follow: follow})
	if err != nil {
		return nil, err
	}

	ch := make(chan *JournalEntry)

	go func() {
		if stopCh == nil {
			return
		}
		<-stopCh
		fmt.Fprintf(os.Stderr, "quitting\n")
		if err := f.StopAtEOF(); err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
		}
	}()

	go func() {
		defer close(ch)
		defer f.Cleanup()

		for line := range f.Lines {
			if err := line.Err; err != nil {
				fmt.Fprintf(os.Stderr, "err: %v\n", err)
				return
			}
			var entry JournalEntry
			if err := json.Unmarshal([]byte(line.Text), &entry); err != nil {
				fmt.Fprintf(os.Stderr, "err: %v\n", err)
				return
			}

			ch <- &entry
		}
	}()

	return ch, nil
}
