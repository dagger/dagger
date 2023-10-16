package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/vito/midterm"
	"github.com/vito/progrock"
)

var shellIsContainer bool

var shellCmd = &FuncCommand{
	Name:  "shell",
	Short: "Open a shell in a container",
	OnSelectObject: func(c *callContext, name string) (*modTypeDef, error) {
		if name == Container {
			c.Select("id")
			shellIsContainer = true
			return &modTypeDef{Kind: dagger.Stringkind}, nil
		}
		return nil, nil
	},
	CheckReturnType: func(_ *callContext, _ *modTypeDef) error {
		if !shellIsContainer {
			return fmt.Errorf("shell can only be called on a container")
		}
		return nil
	},
	OnResult: func(c *callContext, cmd *cobra.Command, returnType modTypeDef, result *any) error {
		ctrID, ok := (*result).(string)
		if !ok {
			return fmt.Errorf("unexpected type %T", result)
		}

		ctx := cmd.Context()
		ctr := c.e.Dagger().Container(dagger.ContainerOpts{
			ID: dagger.ContainerID(ctrID),
		})

		shellEndpoint, err := ctr.ShellEndpoint(ctx)
		if err != nil {
			return fmt.Errorf("failed to get shell endpoint: %w", err)
		}

		return attachToShell(ctx, c.e, shellEndpoint)
	},
}

func attachToShell(ctx context.Context, engineClient *client.Client, shellEndpoint string) (rerr error) {
	rec := progrock.FromContext(ctx)

	dialer := &websocket.Dialer{
		NetDialContext: engineClient.DialContext,
	}
	wsconn, errResp, err := dialer.DialContext(ctx, shellEndpoint, nil)
	if err != nil {
		return err
	}
	// wsconn is closed as part of the caller closing engineClient
	if errResp != nil {
		defer errResp.Body.Close()
	}

	shellStdinR, shellStdinW := io.Pipe()

	vtx := rec.Vertex("shell", "shell",
		progrock.Focused(),
		progrock.Zoomed(func(term *midterm.Terminal) io.Writer {
			term.ForwardRequests = os.Stderr
			term.ForwardResponses = shellStdinW
			term.CursorVisible = true
			term.OnResize(func(h, w int) {
				message := []byte(engine.ResizePrefix)
				message = append(message, []byte(fmt.Sprintf("%d;%d", w, h))...)
				// best effort
				_ = wsconn.WriteMessage(websocket.BinaryMessage, message)
			})

			return shellStdinW
		}))
	defer func() {
		vtx.Done(rerr)
	}()

	// Handle incoming messages
	errCh := make(chan error)
	exitCode := 1
	go func() {
		defer close(errCh)

		for {
			_, buff, err := wsconn.ReadMessage()
			if err != nil {
				errCh <- fmt.Errorf("read: %w", err)
				return
			}
			switch {
			case bytes.HasPrefix(buff, []byte(engine.StdoutPrefix)):
				vtx.Stdout().Write(bytes.TrimPrefix(buff, []byte(engine.StdoutPrefix)))
			case bytes.HasPrefix(buff, []byte(engine.StderrPrefix)):
				vtx.Stderr().Write(bytes.TrimPrefix(buff, []byte(engine.StderrPrefix)))
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
			n, err := shellStdinR.Read(b)
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
		wsCloseErr := &websocket.CloseError{}
		if errors.As(err, &wsCloseErr) && wsCloseErr.Code == websocket.CloseNormalClosure {
			return nil
		}
		return fmt.Errorf("websocket close: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("exited with code %d", exitCode)
	}

	return nil
}
