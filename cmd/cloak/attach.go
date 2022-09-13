package main

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/containerd/console"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	stdinPrefix  = []byte{0, byte(',')}
	stdoutPrefix = []byte{1, byte(',')}
	stderrPrefix = []byte{2, byte(',')}
	resizePrefix = []byte("resize,")
	exitPrefix   = []byte("exit,")
)

var attachCmd = &cobra.Command{
	Use:  "attach",
	Run:  Attach,
	Args: cobra.ExactArgs(1),
}

func Attach(cmd *cobra.Command, args []string) {
	id := args[0]

	exitCode, err := attachSession(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}

	os.Exit(exitCode)
}

func attachSession(id string) (int, error) {
	// FIXME: hardcoded address
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:8080/ws/services/"+id, nil)
	if err != nil {
		return 1, err
	}
	defer conn.Close()

	// Handle terminal sizing
	current := console.Current()
	sendTermSize := func() {
		var (
			width  = 80
			height = 120
		)
		size, err := current.Size()
		if err == nil {
			width, height = int(size.Width), int(size.Height)
		}
		message := append([]byte{}, resizePrefix...)
		message = append(message, []byte(fmt.Sprintf("%d;%d", width, height))...)
		conn.WriteMessage(websocket.BinaryMessage, message)
	}
	// Send the current terminal size right away (initial sizing)
	sendTermSize()
	// Send updates as terminal gets resized
	sigWinch := make(chan os.Signal, 1)
	signal.Notify(sigWinch, syscall.SIGWINCH)
	go func() {
		for range sigWinch {
			sendTermSize()
		}
	}()

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return 1, err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Handle incoming messages
	errCh := make(chan error)
	exitCode := 1
	go func() {
		defer close(errCh)

		for {
			_, buff, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			switch {
			case bytes.HasPrefix(buff, stdoutPrefix):
				os.Stdout.Write(bytes.TrimPrefix(buff, stdoutPrefix))
			case bytes.HasPrefix(buff, stderrPrefix):
				os.Stderr.Write(bytes.TrimPrefix(buff, stderrPrefix))
			case bytes.HasPrefix(buff, exitPrefix):
				code, err := strconv.Atoi(string(bytes.TrimPrefix(buff, exitPrefix)))
				if err == nil {
					exitCode = code
				}
			}
		}
	}()

	// Forward stdin to websockets
	go func() {
		for {
			b := make([]byte, 512)

			n, err := os.Stdin.Read(b)
			if err != nil {
				fmt.Fprintf(os.Stderr, "read: %v\n", err)
				continue
			}
			message := append([]byte{}, stdinPrefix...)
			message = append(message, b[:n]...)
			err = conn.WriteMessage(websocket.BinaryMessage, message)
			if err != nil {
				fmt.Fprintf(os.Stderr, "write: %v\n", err)
				continue
			}
		}
	}()

	if err := <-errCh; err != nil {
		if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			return exitCode, err
		}
	}
	return exitCode, nil
}
