package client

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Hook struct {
	Name string
	Env  map[string]string
}

func (hook Hook) Path() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(configDir, "dagger", "hooks", hook.Name)
}
func (hook Hook) Exists() bool {
	info, err := os.Stat(hook.Path())
	if err != nil {
		return false
	}

	// Check if the file is executable
	mode := info.Mode()
	return mode&0111 != 0
}

func (hook Hook) Connect(ctx context.Context) (net.Conn, error) {
	cmd := exec.CommandContext(ctx, hook.Path())
	// Inject hook.Env into the command environment
	if hook.Env != nil {
		env := os.Environ()
		for k, v := range hook.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	cmd.Stderr = os.Stderr

	conn := &stdioConn{
		stdin:  stdin,
		stdout: stdout,
		cmd:    cmd,
	}

	return conn, nil
}

type stdioConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
}

func (c *stdioConn) Read(b []byte) (n int, err error) {
	return c.stdout.Read(b)
}

func (c *stdioConn) Write(b []byte) (n int, err error) {
	return c.stdin.Write(b)
}

func (c *stdioConn) Close() error {
	c.stdin.Close()
	c.stdout.Close()
	return c.cmd.Wait()
}

func (c *stdioConn) LocalAddr() net.Addr {
	return &net.UnixAddr{Name: "stdio", Net: "stdio"}
}

func (c *stdioConn) RemoteAddr() net.Addr {
	return &net.UnixAddr{Name: "stdio", Net: "stdio"}
}

func (c *stdioConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *stdioConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *stdioConn) SetWriteDeadline(t time.Time) error {
	return nil
}
