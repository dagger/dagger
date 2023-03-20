package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

var journalFile string

func init() {
	flag.StringVar(&journalFile, "journal", os.Getenv("_EXPERIMENTAL_DAGGER_JOURNAL"), "replay journal file")
}

func main() {
	flag.Parse()

	cmd := flag.Args()
	if len(cmd) == 0 && journalFile == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s ([cmd...] | --journal <file>)\n", os.Args[0])
		os.Exit(1)
	}

	if journalFile == "" {
		tmp, err := os.CreateTemp("", "journal*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
			os.Exit(1)
		}

		journalFile = tmp.Name()
	}

	ch, err := tailJournal(journalFile, true, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := tea.NewProgram(New(cancel, ch), tea.WithAltScreen())

	if len(cmd) > 0 {
		cmd := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
		cmd.Env = append(os.Environ(), "_EXPERIMENTAL_DAGGER_JOURNAL="+journalFile)

		err := cmd.Start()
		if err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
			os.Exit(1)
		}

		defer cmd.Wait()
	}

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
