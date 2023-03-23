package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/internal/engine/journal"
)

var journalFile string

func init() {
	flag.StringVar(&journalFile, "journal", os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL"), "replay journal file")
}

func main() {
	flag.Parse()

	if err := run(flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd []string) error {
	if len(cmd) == 0 && journalFile == "" {
		return fmt.Errorf("usage: %s ([cmd...] | --journal <file>)", os.Args[0])
	}

	var r journal.Reader
	var err error
	if journalFile != "" {
		r, err = tailJournal(journalFile, true, nil)
		if err != nil {
			return fmt.Errorf("tail: %w", err)
		}
	} else {
		sink, err := journal.ServeWriters("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		defer sink.Close()

		r = sink

		journalFile = sink.Endpoint()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := tea.NewProgram(New(cancel, r), tea.WithAltScreen())

	if len(cmd) > 0 {
		cmd := exec.CommandContext(ctx, cmd[0], cmd[1:]...) // nolint:gosec
		cmd.Env = append(os.Environ(), "_EXPERIMENTAL_DAGGER_JOURNAL="+journalFile)

		// NB: go run lets its child process roam free when you interrupt it, so
		// make sure they all get signalled. (you don't normally notice this in a
		// shell because Ctrl+C sends to the process group.)
		ensureChildProcessesAreKilled(cmd)

		err := cmd.Start()
		if err != nil {
			return fmt.Errorf("start command: %w", err)
		}

		defer cmd.Wait()
	}

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("run UI: %w", err)
	}

	return nil
}
