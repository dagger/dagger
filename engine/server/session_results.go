package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dagger/dagger/engine"
)

const terminalSessionTombstoneTTL = time.Minute

func (srv *Server) initializeSessionResultsRoot() error {
	root := filepath.Join(srv.workerRootDir, "session-results")
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove stale detachable session results: %w", err)
	}
	srv.processID = strconv.Itoa(os.Getpid())
	srv.sessionResultsRoot = filepath.Join(root, srv.processID)
	if err := os.MkdirAll(srv.sessionResultsRoot, 0o700); err != nil {
		return fmt.Errorf("create detachable session result root: %w", err)
	}
	if err := os.Chmod(srv.sessionResultsRoot, 0o700); err != nil {
		return fmt.Errorf("secure detachable session result root: %w", err)
	}
	return nil
}

func (srv *Server) sessionResultDir(sessionID string) (string, error) {
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return "", fmt.Errorf("invalid session result ID: %w", err)
	}
	if srv.sessionResultsRoot == "" {
		return "", errors.New("session result root is not initialized")
	}
	return filepath.Join(srv.sessionResultsRoot, sessionID), nil
}

func (srv *Server) removeSessionResults(sessionID string) error {
	dir, err := srv.sessionResultDir(sessionID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove detachable session results: %w", err)
	}
	return nil
}

func (srv *Server) nowTime() time.Time {
	if srv.now != nil {
		return srv.now()
	}
	return time.Now()
}

func (srv *Server) recordTerminalSessionID(sessionID string) {
	now := srv.nowTime()
	srv.terminalSessionIDsMu.Lock()
	defer srv.terminalSessionIDsMu.Unlock()
	if srv.terminalSessionIDs == nil {
		srv.terminalSessionIDs = map[string]time.Time{}
	}
	for id, expiry := range srv.terminalSessionIDs {
		if !now.Before(expiry) {
			delete(srv.terminalSessionIDs, id)
		}
	}
	srv.terminalSessionIDs[sessionID] = now.Add(terminalSessionTombstoneTTL)
}

func (srv *Server) terminalSessionIDExists(sessionID string) bool {
	now := srv.nowTime()
	srv.terminalSessionIDsMu.Lock()
	defer srv.terminalSessionIDsMu.Unlock()
	expiry, ok := srv.terminalSessionIDs[sessionID]
	if !ok {
		return false
	}
	if !now.Before(expiry) {
		delete(srv.terminalSessionIDs, sessionID)
		return false
	}
	return true
}
