//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/h2c"
)

func main() {
	var err error
	switch os.Args[0] {
	case "/.init":
		err = mainInit()
	case "/proc/self/exe":
		err = mainSession()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func mainInit() error {
	sigCh := make(chan os.Signal, 16)
	// Handle every signal other than a few exceptions noted at the end.
	// Importantly, by handling all these signals, the child process will start with
	// the default signal disposition for them after the exec, which is what we want.
	// https://man7.org/linux/man-pages/man7/signal.7.html
	signal.Notify(sigCh,
		syscall.SIGABRT,
		syscall.SIGALRM,
		syscall.SIGBUS,
		syscall.SIGCHLD,
		syscall.SIGCLD,
		syscall.SIGCONT,
		syscall.SIGFPE,
		syscall.SIGHUP,
		syscall.SIGILL,
		syscall.SIGINT,
		syscall.SIGIO,
		syscall.SIGIOT,
		syscall.SIGPIPE,
		syscall.SIGPOLL,
		syscall.SIGPROF,
		syscall.SIGPWR,
		syscall.SIGQUIT,
		syscall.SIGSEGV,
		syscall.SIGSTKFLT,
		syscall.SIGSYS,
		syscall.SIGTERM,
		syscall.SIGTRAP,
		syscall.SIGTSTP,
		syscall.SIGTTIN,
		syscall.SIGTTOU,
		syscall.SIGUNUSED,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
		syscall.SIGVTALRM,
		syscall.SIGWINCH,
		syscall.SIGXCPU,
		syscall.SIGXFSZ,
		// explicitly not caught
		// syscall.SIGKILL, // cannot be caught
		// syscall.SIGSTOP, // cannot be caught
		// syscall.SIGURG, // go runtime uses this internally
	)

	// try to detach from the terminal, if there is one
	// if detach successful, remember to ignore first SIGHUP/SIGCONT later (they might not be sent immediately and shouldn't be forwarded to the child)

	_, err := unix.IoctlGetTermios(0, unix.TCGETS)
	haveTTY := err == nil

	sid, err := unix.Getsid(0)
	if err != nil {
		return err
	}
	pid := unix.Getpid()
	weAreSessionLeader := sid == pid

	var ignoreFirstHUP bool
	var ignoreFirstCONT bool
	if haveTTY && weAreSessionLeader {
		ignoreFirstHUP = true
		ignoreFirstCONT = true
		_, err = unix.IoctlRetInt(0, unix.TIOCNOTTY)
		if err != nil {
			return err
		}
	}

	if _, ok := os.LookupEnv("DAGGER_SESSION_TOKEN"); ok {
		if err := startSessionSubprocess(); err != nil {
			return err
		}
	}

	// run the child in a new session
	sysProcAttr := syscall.SysProcAttr{
		Setsid: true,
	}
	if haveTTY {
		sysProcAttr.Setctty = true
		sysProcAttr.Ctty = 0
	}

	// start the child process
	fullPath := os.Args[1]
	if filepath.Base(fullPath) == fullPath {
		// search for the executable in $PATH
		fullPath, err = exec.LookPath(fullPath)
		if errors.Is(err, exec.ErrDot) {
			// NOTE: backwards compat with dumb-init
			err = nil
		}
		if err != nil {
			return err
		}
	}
	child, err := os.StartProcess(fullPath, os.Args[1:], &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Sys:   &sysProcAttr,
	})
	if err != nil {
		return err
	}

	// handle signals until our child exits
	for sig := range sigCh {
		sigNum := sig.(syscall.Signal)

		var goToBed bool
		switch sigNum {
		case syscall.SIGHUP:
			if ignoreFirstHUP {
				ignoreFirstHUP = false
				continue
			}
		case syscall.SIGCONT:
			if ignoreFirstCONT {
				ignoreFirstCONT = false
				continue
			}

		case syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU:
			sigNum = syscall.SIGSTOP
			goToBed = true

		case syscall.SIGCHLD:
			// reap what we have sown (aka various zombie children)
			for {
				var ws syscall.WaitStatus
				deadPid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
				if err != nil || deadPid == 0 {
					break
				}
				if deadPid == child.Pid {
					// our child died, so we should too
					exitStatus := ws.ExitStatus()
					if exitStatus == -1 {
						exitStatus = 128 + int(ws.Signal())
					}

					// send SIGTERM to anyone left
					unix.Kill(-child.Pid, syscall.SIGTERM)

					// goodbye
					os.Exit(exitStatus)
				}
			}

			continue
		}

		// forward the signal to the child's process group
		unix.Kill(-child.Pid, sigNum) // ignore error, best effort

		if goToBed {
			unix.Kill(pid, syscall.SIGSTOP)
		}
	}

	return nil
}

func startSessionSubprocess() error {
	// create a pipe to synchronize with the child process on when the session has started
	// when the child closes the write end of the pipe, we know it has started (or died, which
	// will result in errors for the nested exec process on any use of a session attachable)
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}

	// start the session subprocess
	cmd := exec.Command("/proc/self/exe")

	// forwarding our stdio ensures that a panic in the child process won't get hidden and any other logging works too
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.ExtraFiles = []*os.File{w}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start session subprocess: %w", err)
	}

	// wait for the session attachables to be ready (or the child to die)

	// need to close our dup of the write end of the pipe
	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close pipe: %w", err)
	}

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		io.Copy(io.Discard, r)
	}()
	// something really really wrong would have to happen for this to block indefinitely, but be
	// cautious anyways w/ an overly generous timeout
	select {
	case <-doneCh:
		return nil
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("timed out waiting for session subprocess to start")
	}
}

func mainSession() error {
	ctx := context.Background()

	// this is closed when the session server is about to run, letting the parent process know that
	pipeW := os.NewFile(3, "session-pipe-w")

	portStr, ok := os.LookupEnv("DAGGER_SESSION_PORT")
	if !ok {
		return fmt.Errorf("DAGGER_SESSION_PORT not set")
	}
	_, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("DAGGER_SESSION_PORT invalid: %w", err)
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+portStr)
	if err != nil {
		return fmt.Errorf("failed to connect to session server: %w", err)
	}

	attachables := []bksession.Attachable{
		// secrets
		secretprovider.NewSecretProvider(),
		// sockets
		client.SocketProvider{EnableHostNetworkAccess: true},
		// host=>container networking
		h2c.NewTunnelListenerAttachable(ctx),
		// Git attachable
		git.NewGitAttachable(ctx),
	}
	// filesync
	filesyncer, err := client.NewFilesyncer()
	if err != nil {
		return err
	}
	attachables = append(attachables, filesyncer.AsSource(), filesyncer.AsTarget())

	sessionSrv, err := client.ConnectBuildkitSession(ctx, conn, http.Header{}, attachables...)
	if err != nil {
		return err
	}
	defer sessionSrv.Stop()

	if err := pipeW.Close(); err != nil {
		return err
	}
	return sessionSrv.Run(ctx)
}
