package integration

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/pkg/errors"
)

func NewRegistry(dir string) (url string, cl func() error, err error) {
	if err := LookupBinary("registry"); err != nil {
		return "", nil, err
	}

	deferF := &MultiCloser{}
	cl = deferF.F()

	defer func() {
		if err != nil {
			deferF.F()()
			cl = nil
		}
	}()

	if dir == "" {
		tmpdir, err := os.MkdirTemp("", "test-registry")
		if err != nil {
			return "", nil, err
		}
		deferF.Append(func() error { return os.RemoveAll(tmpdir) })
		dir = tmpdir
	}

	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, err
		}
		template := fmt.Sprintf(`version: 0.1
loglevel: debug
storage:
    filesystem:
        rootdirectory: %s
http:
    addr: 127.0.0.1:0
`, filepath.Join(dir, "data"))

		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(template), 0600); err != nil {
			return "", nil, err
		}
	}

	cmd := exec.Command("registry", "serve", filepath.Join(dir, "config.yaml")) //nolint:gosec // test utility
	rc, err := cmd.StderrPipe()
	if err != nil {
		return "", nil, err
	}
	stop, err := StartCmd(cmd, nil)
	if err != nil {
		return "", nil, err
	}
	deferF.Append(stop)

	ctx, cancel := context.WithCancelCause(context.Background())
	ctx, _ = context.WithTimeoutCause(ctx, 5*time.Second, errors.WithStack(context.DeadlineExceeded))
	defer cancel(errors.WithStack(context.Canceled))
	url, err = detectPort(ctx, rc)
	if err != nil {
		return "", nil, err
	}

	return
}

func detectPort(ctx context.Context, rc io.ReadCloser) (string, error) {
	r := regexp.MustCompile(`listening on 127\.0\.0\.1:(\d+)`)
	s := bufio.NewScanner(rc)
	found := make(chan struct{})
	defer func() {
		close(found)
		go io.Copy(io.Discard, rc)
	}()

	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-found:
				return
			default:
				rc.Close()
			}
		case <-found:
		}
	}()

	for s.Scan() {
		res := r.FindSubmatch(s.Bytes())
		if len(res) > 1 {
			return "localhost:" + string(res[1]), nil
		}
	}
	return "", errors.Errorf("no listening address found")
}
