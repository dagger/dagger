package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	bkclient "github.com/moby/buildkit/client"
)

func loadEvents(journal string) chan *bkclient.SolveStatus {
	f, err := os.Open(journal)
	if err != nil {
		panic(err)
	}

	decoder := json.NewDecoder(f)
	ch := make(chan *bkclient.SolveStatus)

	var prevTS time.Time
	go func() {
		defer close(ch)

		for {
			entry := struct {
				Event *bkclient.SolveStatus
				TS    time.Time
			}{}

			err := decoder.Decode(&entry)
			if err == io.EOF {
				break
			}

			if err != nil {
				panic(err)
			}

			if !prevTS.IsZero() {
				time.Sleep(entry.TS.Sub(prevTS))
			}
			prevTS = entry.TS

			ch <- entry.Event
		}
	}()

	return ch
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <file>\n", os.Args[0])
		os.Exit(1)
	}
	ch := loadEvents(os.Args[1])
	p := tea.NewProgram(New(ch), tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
