package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	bkclient "github.com/moby/buildkit/client"
	"github.com/nxadm/tail"
)

type JournalEntry struct {
	Event *bkclient.SolveStatus
	TS    time.Time
}

func tailJournal(journal string, follow bool, stopCh chan struct{}) (chan *bkclient.SolveStatus, error) {
	f, err := tail.TailFile(journal, tail.Config{Follow: follow})
	if err != nil {
		return nil, err
	}

	ch := make(chan *bkclient.SolveStatus)

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

			ch <- entry.Event
		}
	}()

	return ch, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <file>\n", os.Args[0])
		os.Exit(1)
	}
	ch, err := tailJournal(os.Args[1], true, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		os.Exit(1)
	}
	p := tea.NewProgram(New(ch), tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
