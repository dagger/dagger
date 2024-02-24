package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/gorilla/websocket"
)

func attachToShell(ctx context.Context, engineClient *client.Client, shellEndpoint string) (rerr error) {
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

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

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
			n, err := stdin.Read(b)
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
		return fmt.Errorf("exited with code %d", exitCode)
	}

	return nil
}
