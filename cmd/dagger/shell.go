package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
	"github.com/mattn/go-isatty"
	"golang.org/x/term"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
)

func attachToShell(ctx context.Context, engineClient *client.Client, shellEndpoint string) (rerr error) {
	return Frontend.Background(&terminalSession{
		Ctx:      ctx,
		Client:   engineClient,
		Endpoint: shellEndpoint,
	})
}

type terminalSession struct {
	Ctx      context.Context
	Client   *client.Client
	Endpoint string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

var _ tea.ExecCommand = (*terminalSession)(nil)

func (ts *terminalSession) SetStdin(r io.Reader) {
	ts.stdin = r
}

func (ts *terminalSession) SetStdout(w io.Writer) {
	ts.stdout = w
}

func (ts *terminalSession) SetStderr(w io.Writer) {
	ts.stderr = w
}

func (ts *terminalSession) Run() error {
	dialer := &websocket.Dialer{
		NetDialContext: ts.Client.DialContext,
	}

	reqHeader := http.Header{}
	if ts.Client.SecretToken != "" {
		reqHeader["Authorization"] = []string{"Basic " + base64.StdEncoding.EncodeToString([]byte(ts.Client.SecretToken+":"))}
	}

	wsconn, errResp, err := dialer.DialContext(ts.Ctx, ts.Endpoint, reqHeader)
	if err != nil {
		if errors.Is(err, websocket.ErrBadHandshake) {
			return fmt.Errorf("dial error %d: %w", errResp.StatusCode, err)
		}
		return fmt.Errorf("dial: %w", err)
	}

	if err := ts.sendSize(wsconn); err != nil {
		return fmt.Errorf("sending initial size: %w", err)
	}

	go ts.listenForResize(wsconn)

	// wsconn is closed as part of the caller closing ts.client
	if errResp != nil {
		defer errResp.Body.Close()
	}

	buf := new(bytes.Buffer)
	stdout := io.MultiWriter(buf, ts.stdout)
	stderr := io.MultiWriter(buf, ts.stdout)

	// Handle incoming messages
	errCh := make(chan error)
	exitCode := 1
	go func() {
		defer close(errCh)

		for {
			_, buff, err := wsconn.ReadMessage()
			if err != nil {
				wsCloseErr := &websocket.CloseError{}
				if errors.As(err, &wsCloseErr) && wsCloseErr.Code == websocket.CloseNormalClosure {
					break
				} else {
					errCh <- fmt.Errorf("read: %w", err)
				}
				return
			}
			switch {
			case bytes.HasPrefix(buff, []byte(engine.StdoutPrefix)):
				stdout.Write(bytes.TrimPrefix(buff, []byte(engine.StdoutPrefix)))
			case bytes.HasPrefix(buff, []byte(engine.StderrPrefix)):
				stderr.Write(bytes.TrimPrefix(buff, []byte(engine.StderrPrefix)))
			case bytes.HasPrefix(buff, []byte(engine.ExitPrefix)):
				code, err := strconv.Atoi(string(bytes.TrimPrefix(buff, []byte(engine.ExitPrefix))))
				if err == nil {
					exitCode = code
				}
			}
		}
	}()

	// Forward stdin to websockets
	go func() {
		b := make([]byte, 512)

		for {
			n, err := ts.stdin.Read(b)
			if err != nil {
				fmt.Fprintf(os.Stderr, "read: %v\n", err)
				continue
			}
			message := []byte(engine.StdinPrefix)
			message = append(message, b[:n]...)
			err = wsconn.WriteMessage(websocket.BinaryMessage, message)
			if err != nil {
				fmt.Fprintf(os.Stderr, "write: %v\n", err)
				continue
			}
		}
	}()

	if err := <-errCh; err != nil {
		return fmt.Errorf("websocket error: %w", err)
	}

	if exitCode != 0 {
		return fmt.Errorf("exited with code %d\n\nOutput:\n\n%s", exitCode, buf.String())
	}

	return nil
}

func (ts *terminalSession) sendSize(wsconn *websocket.Conn) error {
	f, ok := ts.stdout.(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		slog.Debug("stdin is not a terminal; cannot get terminal size")
		return nil
	}

	w, h, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return fmt.Errorf("get terminal size: %w", err)
	}

	message := []byte(engine.ResizePrefix)
	message = append(message, []byte(fmt.Sprintf("%d;%d", w, h))...)
	if err := wsconn.WriteMessage(websocket.BinaryMessage, message); err != nil {
		return fmt.Errorf("send resize message: %w", err)
	}

	return nil
}
