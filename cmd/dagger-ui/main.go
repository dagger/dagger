package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/internal/engine"
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

	var r engine.JournalReader
	var err error
	if journalFile != "" {
		r, err = tailJournal(journalFile, true, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
			os.Exit(1)
		}
	} else {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
			os.Exit(1)
		}

		r, _, err = engine.ServeRPC(l)
		if err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
			os.Exit(1)
		}

		journalFile = fmt.Sprintf("tcp://%s", l.Addr())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := tea.NewProgram(New(cancel, r), tea.WithAltScreen())

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
