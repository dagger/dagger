package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dagger/dagger/telemetry"
	bkclient "github.com/moby/buildkit/client"
	"github.com/nxadm/tail"
	"github.com/vito/progrock"
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

	t, url, ok := telemetry.NewWriter()
	if !ok {
		fmt.Fprintln(os.Stderr, "telemetry token not configured")
		os.Exit(1)
	}

	fmt.Println("Dagger Cloud url:", url)

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
	t.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		os.Exit(1)
	}
}

func processJournal(w progrock.Writer, updates chan *progrock.StatusUpdate) error {
	for update := range updates {
		if err := w.WriteStatus(update); err != nil {
			return err
		}
	}

	return nil
}

type JournalEntry struct {
	Event *bkclient.SolveStatus
	TS    time.Time
}

func tailJournal(journal string, follow bool, stopCh chan struct{}) (chan *progrock.StatusUpdate, error) {
	f, err := tail.TailFile(journal, tail.Config{Follow: follow})
	if err != nil {
		return nil, err
	}

	ch := make(chan *progrock.StatusUpdate)

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
			var entry progrock.StatusUpdate
			if err := json.Unmarshal([]byte(line.Text), &entry); err != nil {
				fmt.Fprintf(os.Stderr, "err: %v\n", err)
				return
			}

			ch <- &entry
		}
	}()

	return ch, nil
}
