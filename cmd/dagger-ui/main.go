package main

import (
	"context"
	"flag"
	"fmt"
	"net"
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

	cmd := flag.Args()
	if len(cmd) == 0 && journalFile == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s ([cmd...] | --journal <file>)\n", os.Args[0])
		os.Exit(1)
	}

	var r journal.Reader
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

		sink, err := journal.ServeWriters(l)
		if err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
			os.Exit(1)
		}

		defer sink.Flush()

		r = sink

		journalFile = fmt.Sprintf("tcp://%s", l.Addr())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := tea.NewProgram(New(cancel, r), tea.WithAltScreen())

	if len(cmd) > 0 {
		cmd := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
		cmd.Env = append(os.Environ(), "_EXPERIMENTAL_DAGGER_JOURNAL="+journalFile)

		// NB: mostly a dev convenience. go run lets its child process roam free
		// when you interrupt it, so make sure they all get signalled. (you don't
		// normally notice this in a shell because Ctrl+C sends to the process
		// group.)
		ensureChildProcessesAreKilled(cmd)

		err := cmd.Start()
		if err != nil {
			fmt.Fprintf(os.Stderr, "err: %v\n", err)
			os.Exit(1)
		}

		go cmd.Wait()
	}

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
