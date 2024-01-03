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

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/vito/midterm"
	"github.com/vito/progrock"
)

var shellEntrypoint []string

var shellCmd = &FuncCommand{
	Name:  "shell",
	Short: "Open a shell in a container",
	Long:  "Open a shell in a container.\n\nIf no entrypoint is specified and the container doesn't have a default command, `sh` will be used.",
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().StringSliceVar(&shellEntrypoint, "entrypoint", nil, "entrypoint to use")
	},
	OnSelectObjectLeaf: func(c *FuncCommand, name string) error {
		if name != Container {
			return fmt.Errorf("shell can only be called on a container, not %q", name)
		}
		c.Select("id")
		return nil
	},
	BeforeRequest: func(_ *FuncCommand, _ *cobra.Command, _ *modTypeDef) error {
		// Even though these flags are global, we only check them just before query
		// execution because you may want to debug an error during loading or for
		// --help.
		if silent || !(progress == "auto" && autoTTY || progress == "tty") {
			return fmt.Errorf("running shell without the TUI is not supported")
		}
		if debug {
			return fmt.Errorf("running shell with --debug is not supported")
		}
		return nil
	},
	AfterResponse: func(c *FuncCommand, cmd *cobra.Command, returnType *modTypeDef, response any) error {
		ctrID, ok := (response).(string)
		if !ok {
			return fmt.Errorf("unexpected response %T: %+v", response, response)
		}

		ctx := cmd.Context()
		ctr := c.c.Dagger().Container(dagger.ContainerOpts{
			ID: dagger.ContainerID(ctrID),
		})

		shellEndpoint, err := withShellExec(ctx, ctr).ShellEndpoint(ctx)
		if err != nil {
			return fmt.Errorf("failed to get shell endpoint: %w", err)
		}

		return attachToShell(ctx, c.c, shellEndpoint)
	},
}

func withShellExec(ctx context.Context, ctr *dagger.Container) *dagger.Container {
	args := shellEntrypoint

	if len(args) == 0 {
		args, _ = ctr.Entrypoint(ctx)

		if len(args) == 0 {
			args, _ = ctr.DefaultArgs(ctx)
		}

		if len(args) > 0 {
			return ctr.WithExec(nil)
		}

		args = []string{"sh"}
	}

	return ctr.WithExec(args, dagger.ContainerWithExecOpts{
		SkipEntrypoint: true,
	})
}

func attachToShell(ctx context.Context, engineClient *client.Client, shellEndpoint string) (rerr error) {
	rec := progrock.FromContext(ctx)

	dialer := &websocket.Dialer{
		NetDialContext: engineClient.DialContext,
	}

	reqHeader := http.Header{}
	if engineClient.SecretToken != "" {
		reqHeader["Authorization"] = []string{"Basic " + base64.StdEncoding.EncodeToString([]byte(engineClient.SecretToken+":"))}
	}

	wsconn, errResp, err := dialer.DialContext(ctx, shellEndpoint, reqHeader)
	if err != nil {
		if errors.Is(err, websocket.ErrBadHandshake) {
			return fmt.Errorf("dial error %d: %w", errResp.StatusCode, err)
		}
		return fmt.Errorf("dial: %w", err)
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
