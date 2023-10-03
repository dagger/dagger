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
	"github.com/dagger/dagger/engine/client"
	"github.com/gorilla/websocket"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/vito/midterm"
	"github.com/vito/progrock"
)

var shellCmd = &cobra.Command{
	Use:                   "shell",
	DisableFlagsInUseLine: true,
	Hidden:                true, // for now, remove once we're ready for primetime
	RunE: func(cmd *cobra.Command, args []string) error {
		focus = queryFocus
		return loadModCmdWrapper(RunShell, "", true)(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(shellCmd)

	shellCmd.Flags().StringVar(&queryFile, "doc", "", "document query file")
	shellCmd.Flags().StringSliceVar(&queryVarsInput, "var", nil, "query variable")
	shellCmd.Flags().StringVar(&queryVarsJSONInput, "var-json", "", "json query variables (overrides --var)")
}

var (
	// TODO:dedupe w/ same thing in core
	stdinPrefix  = []byte{0, byte(',')}
	stdoutPrefix = []byte{1, byte(',')}
	stderrPrefix = []byte{2, byte(',')}
	resizePrefix = []byte("resize,")
	exitPrefix   = []byte("exit,")
)

func RunShell(
	ctx context.Context,
	engineClient *client.Client,
	_ *dagger.Module,
	cmd *cobra.Command,
	dynamicCmdArgs []string,
) (err error) {
	rec := progrock.RecorderFromContext(ctx)
	vtx := rec.Vertex(
		digest.Digest("shell"),
		"shell",
	)
	defer func() { vtx.Done(err) }()

	cmd.SetOut(vtx.Stdout())
	cmd.SetErr(vtx.Stderr())

	res, err := runQuery(ctx, engineClient, dynamicCmdArgs)
	if err != nil {
		return fmt.Errorf("failed to run query: %w", err)
	}

	var getCtrID func(x any) (string, error)
	getCtrID = func(x any) (string, error) {
		switch x := x.(type) {
		case map[string]any:
			for _, v := range x {
				return getCtrID(v)
			}
			return "", fmt.Errorf("no container found")
		case string:
			return x, nil
		default:
			return "", fmt.Errorf("unexpected type %T", x)
		}
	}
	ctrID, err := getCtrID(res)
	if err != nil {
		return fmt.Errorf("failed to get container id: %w", err)
	}

	dag := engineClient.Dagger()
	ctr := dag.Container(dagger.ContainerOpts{ID: dagger.ContainerID(ctrID)})

	shellEndpoint, err := ctr.ShellEndpoint(ctx)
	if err != nil {
		return fmt.Errorf("failed to get shell endpoint: %w", err)
	}
	return attachToShell(ctx, engineClient, shellEndpoint)
}

func attachToShell(ctx context.Context, engineClient *client.Client, shellEndpoint string) (rerr error) {
	rec := progrock.RecorderFromContext(ctx)

	// TODO:
	// fmt.Fprintf(os.Stderr, "shell endpoint: %s\n", shellEndpoint)

	dialer := &websocket.Dialer{
		// TODO: need use DialNestedContext when, well, you know, nested. Fix in engine client
		NetDialContext: engineClient.DialContext,
		// TODO:
		// HandshakeTimeout: 60 * time.Second, // TODO: made up number
	}
	wsconn, _, err := dialer.DialContext(ctx, shellEndpoint, nil)
	if err != nil {
		return err
	}
	// wsconn is closed as part of the caller closing engineClient

	// TODO:
	// fmt.Fprintf(os.Stderr, "WE ARE SO CONNECTED\n")

	shellStdinR, shellStdinW := io.Pipe()

	vtx := rec.Vertex("shell", "shell",
		progrock.Focused(),
		progrock.Zoomed(func(term *midterm.Terminal) io.Writer {
			term.ForwardRequests = os.Stderr
			term.ForwardResponses = shellStdinW
			term.CursorVisible = true
			term.OnResize(func(h, w int) {
				message := append([]byte{}, resizePrefix...)
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
			case bytes.HasPrefix(buff, stdoutPrefix):
				vtx.Stdout().Write(bytes.TrimPrefix(buff, stdoutPrefix))
			case bytes.HasPrefix(buff, stderrPrefix):
				vtx.Stderr().Write(bytes.TrimPrefix(buff, stderrPrefix))
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
		b := make([]byte, 512)

		for {
			n, err := shellStdinR.Read(b)
			if err != nil {
				fmt.Fprintf(os.Stderr, "read: %v\n", err)
				continue
			}
			message := append([]byte{}, stdinPrefix...)
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
