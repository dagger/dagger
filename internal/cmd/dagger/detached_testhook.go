package daggercmd

import (
	"fmt"
	"io"
	"net"
	"os"
)

const detachedQueryAcknowledgedTestSocketEnv = "_DAGGER_TEST_DETACHED_QUERY_ACK_SOCKET"

// waitAtDetachedQueryAcknowledgedTestBarrier lets the engine-dev integration
// test cut the creator transport on the acknowledged side of the storage
// boundary without depending on process scheduling. It is inert unless the
// private test environment variable names a listening Unix socket.
func waitAtDetachedQueryAcknowledgedTestBarrier(sessionID, queryID string) error {
	socketPath := os.Getenv(detachedQueryAcknowledgedTestSocketEnv)
	if socketPath == "" {
		return nil
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect detached-query test barrier: %w", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "%s %s\n", sessionID, queryID); err != nil {
		return fmt.Errorf("signal detached-query test barrier: %w", err)
	}
	var release [1]byte
	if _, err := io.ReadFull(conn, release[:]); err != nil {
		return fmt.Errorf("wait at detached-query test barrier: %w", err)
	}
	return nil
}
