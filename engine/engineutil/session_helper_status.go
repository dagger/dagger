package engineutil

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// sessionHelperStatus is a pipe on which the injected /.init reports that a
// nested exec's session helper failed to start or exited (see
// distconsts.SessionHelperStatusFDEnv). The helper starts alongside the
// command, so the engine's queries for the exec's client wait for its
// attachables; a report fails those waits at once with the helper's status.
type sessionHelperStatus struct {
	r, w *os.File
}

func newSessionHelperStatus() (*sessionHelperStatus, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("session helper status pipe: %w", err)
	}
	return &sessionHelperStatus{r: r, w: w}, nil
}

// writeEnd returns the pipe's end for /.init, or nil.
func (s *sessionHelperStatus) writeEnd() *os.File {
	if s == nil {
		return nil
	}
	return s.w
}

// watch calls fail with the first report /.init writes. It returns when the
// pipe is closed.
func (s *sessionHelperStatus) watch(fail func(error)) {
	sc := bufio.NewScanner(s.r)
	for sc.Scan() {
		if err := sessionHelperStatusErr(sc.Text()); err != nil {
			fail(err)
			return
		}
	}
}

func (s *sessionHelperStatus) close() {
	if s == nil {
		return
	}
	s.r.Close()
	s.w.Close()
}

// sessionHelperStatusErr turns a report line into the error queries get.
func sessionHelperStatusErr(line string) error {
	kind, detail, _ := strings.Cut(line, " ")
	switch kind {
	case "exited":
		return fmt.Errorf("the Dagger session helper in this container exited with status %s; its error, if any, is in the exec's output", detail)
	case "start-failed":
		return fmt.Errorf("the Dagger session helper in this container failed to start: %s", detail)
	case "":
		return nil
	default:
		return errors.New("the Dagger session helper in this container stopped: " + line)
	}
}
