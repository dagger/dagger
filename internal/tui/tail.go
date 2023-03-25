package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dagger/dagger/internal/engine/journal"
	bkclient "github.com/moby/buildkit/client"
	"github.com/nxadm/tail"
)

type JournalEntry struct {
	Event *bkclient.SolveStatus
	TS    time.Time
}

func tailJournal(path string, follow bool, stopCh chan struct{}) (journal.Reader, error) {
	f, err := tail.TailFile(path, tail.Config{Follow: follow})
	if err != nil {
		return nil, err
	}

	r, w := journal.Pipe()

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
		defer w.Close()
		defer f.Cleanup()

		for line := range f.Lines {
			if err := line.Err; err != nil {
				fmt.Fprintf(os.Stderr, "tail err: %v\n", err)
				return
			}

			var entry journal.Entry
			if err := json.Unmarshal([]byte(line.Text), &entry); err != nil {
				fmt.Fprintf(os.Stderr, "tail unmarshal error (%s): %v\n", line.Text, err)
				var syntaxErr *json.SyntaxError
				if errors.As(err, &syntaxErr) {
					continue
				}
				return
			}

			w.WriteEntry(&entry)
		}
	}()

	return r, nil
}
