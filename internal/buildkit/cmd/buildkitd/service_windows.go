package main

import (
	"log"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"google.golang.org/grpc"
)

const defaultServiceName = "buildkitd"

var (
	serviceNameFlag       string
	registerServiceFlag   bool
	unregisterServiceFlag bool
	logFileFlag           string

	kernel32     = windows.NewLazySystemDLL("kernel32.dll")
	setStdHandle = kernel32.NewProc("SetStdHandle")
	oldStderr    windows.Handle
	panicFile    *os.File
)

// serviceFlags returns an array of flags for configuring buildkitd to run
// as a Windows service under control of SCM.
func serviceFlags() []cli.Flag {
	return []cli.Flag{
		cli.StringFlag{
			Name:  "service-name",
			Usage: "Set the Windows service name",
			Value: defaultServiceName,
		},
		cli.BoolFlag{
			Name:  "register-service",
			Usage: "Register the service and exit",
		},
		cli.BoolFlag{
			Name:  "unregister-service",
			Usage: "Unregister the service and exit",
		},
		cli.BoolFlag{
			Name:   "run-service",
			Usage:  "",
			Hidden: true,
		},
		cli.StringFlag{
			Name:  "log-file",
			Usage: "Path to the buildkitd log file",
		},
	}
}

func registerService() error {
	p, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	c := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  "Buildkitd",
		Description:  "Container image build engine",
	}

	// Configure the service to launch with the arguments that were just passed.
	args := []string{"--run-service"}
	for _, a := range os.Args[1:] {
		if a != "--register-service" && a != "--unregister-service" {
			args = append(args, a)
		}
	}

	s, err := m.CreateService(serviceNameFlag, p, c, args...)
	if err != nil {
		return err
	}
	defer s.Close()

	// See http://stackoverflow.com/questions/35151052/how-do-i-configure-failure-actions-of-a-windows-service-written-in-go
	const (
		scActionNone    = 0
		scActionRestart = 1

		serviceConfigFailureActions = 2
	)

	type serviceFailureActions struct {
		ResetPeriod  uint32
		RebootMsg    *uint16
		Command      *uint16
		ActionsCount uint32
		Actions      uintptr
	}

	type scAction struct {
		Type  uint32
		Delay uint32
	}
	t := []scAction{
		{Type: scActionRestart, Delay: uint32(15 * time.Second / time.Millisecond)},
		{Type: scActionRestart, Delay: uint32(15 * time.Second / time.Millisecond)},
		{Type: scActionNone},
	}
	lpInfo := serviceFailureActions{ResetPeriod: uint32(24 * time.Hour / time.Second), ActionsCount: uint32(3), Actions: uintptr(unsafe.Pointer(&t[0]))}
	err = windows.ChangeServiceConfig2(s.Handle, serviceConfigFailureActions, (*byte)(unsafe.Pointer(&lpInfo)))
	if err != nil {
		return err
	}

	return nil
}

func unregisterService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceNameFlag)
	if err != nil {
		return err
	}
	defer s.Close()

	err = s.Delete()
	if err != nil {
		return err
	}
	return nil
}

// applyPlatformFlags applies platform-specific flags.
func applyPlatformFlags(context *cli.Context) {
	serviceNameFlag = context.GlobalString("service-name")
	if serviceNameFlag == "" {
		serviceNameFlag = defaultServiceName
	}
	for _, v := range []struct {
		name string
		d    *bool
	}{
		{
			name: "register-service",
			d:    &registerServiceFlag,
		},
		{
			name: "unregister-service",
			d:    &unregisterServiceFlag,
		},
	} {
		*v.d = context.GlobalBool(v.name)
	}
	logFileFlag = context.GlobalString("log-file")
}

type handler struct {
	fromsvc chan error
	server  *grpc.Server
}

// registerUnregisterService is an entrypoint early in the daemon startup
// to handle (un-)registering against Windows Service Control Manager (SCM).
// It returns an indication to stop on successful SCM operation, and an error.
func registerUnregisterService(root string) (bool, error) {
	if unregisterServiceFlag {
		if registerServiceFlag {
			return true, errors.Errorf("--register-service and --unregister-service cannot be used together")
		}
		return true, unregisterService()
	}

	if registerServiceFlag {
		return true, registerService()
	}

	isService, err := svc.IsWindowsService()
	if err != nil {
		return true, err
	}

	if isService {
		if err := initPanicFile(filepath.Join(root, "panic.log")); err != nil {
			return true, err
		}
		// The usual advice for Windows services is to either write to a log file or to the windows event
		// log, the former of which we've exposed here via a --log-file flag. We additionally write panic
		// stacks to a panic.log file to diagnose crashes. Below details the two different outcomes if
		// --log-file is specified or not:
		//
		// --log-file is *not* specified.
		// -------------------------------
		// -logrus, the stdlibs logging package and os.Stderr output will go to
		// NUL (Windows' /dev/null equivalent).
		// -Panics will write their stack trace to the panic.log file.
		// -Writing to the handle returned from GetStdHandle(STD_ERROR_HANDLE) will write
		// to the panic.log file as the underlying handle itself has been redirected.
		//
		// --log-file *is* specified
		// -------------------------------
		// -Logging to logrus, the stdlibs logging package or directly to
		// os.Stderr will all go to the log file specified.
		// -Panics will write their stack trace to the panic.log file.
		// -Writing to the handle returned from GetStdHandle(STD_ERROR_HANDLE) will write
		// to the panic.log file as the underlying handle itself has been redirected.
		var f *os.File
		var err error
		if logFileFlag != "" {
			f, err = os.OpenFile(logFileFlag, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return true, errors.Wrapf(err, "open log file %q", logFileFlag)
			}
		} else {
			// Windows services start with NULL stdio handles, and thus os.Stderr and friends will be
			// backed by an os.File with a NULL handle. This means writes to os.Stderr will fail, which
			// isn't a huge issue as we want output to be discarded if the user doesn't ask for the log
			// file. However, writes succeeding but just going to the ether is a much better construct
			// so use devnull instead of relying on writes failing. We use devnull instead of io.Discard
			// as os.Stderr is an os.File and can't be assigned to io.Discard.
			f, err = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				return true, err
			}
		}
		// Reassign os.Stderr to the log file or NUL. Shim logs are copied to os.Stderr
		// directly so this ensures those will end up in the log file as well if specified.
		os.Stderr = f
		// Assign the stdlibs log package in case of any miscellaneous uses by
		// dependencies.
		log.SetOutput(f)
		logrus.SetOutput(f)
	}
	return false, nil
}

// launchService is the entry point for running the daemon under SCM.
func launchService(s *grpc.Server) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return nil
	}

	h := &handler{
		fromsvc: make(chan error),
		server:  s,
	}

	go func() {
		h.fromsvc <- svc.Run(serviceNameFlag, h)
	}()

	// Wait for the first signal from the service handler.
	err = <-h.fromsvc
	if err != nil {
		return err
	}
	return nil
}

// Execute implements the svc.Handler interface
func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending, Accepts: 0}
	// Unblock launchService()
	h.fromsvc <- nil
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

Loop:
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending, Accepts: 0}
			// this should unblock serveGRPC() which will return control
			// back to the main app, gracefully stopping everything else.
			h.server.Stop()
			break Loop
		}
	}

	removePanicFile()
	return false, 0
}

func initPanicFile(path string) error {
	var err error
	panicFile, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	st, err := panicFile.Stat()
	if err != nil {
		return err
	}

	// If there are contents in the file already, move the file out of the way
	// and replace it.
	if st.Size() > 0 {
		panicFile.Close()
		os.Rename(path, path+".old")
		panicFile, err = os.Create(path)
		if err != nil {
			return err
		}
	}

	// Update STD_ERROR_HANDLE to point to the panic file so that Go writes to
	// it when it panics. Remember the old stderr to restore it before removing
	// the panic file.
	sh := uint32(windows.STD_ERROR_HANDLE)
	h, err := windows.GetStdHandle(sh)
	if err != nil {
		return err
	}

	oldStderr = h

	r, _, err := setStdHandle.Call(uintptr(sh), panicFile.Fd())
	if r == 0 && err != nil {
		return err
	}

	return nil
}

func removePanicFile() {
	if st, err := panicFile.Stat(); err == nil {
		if st.Size() == 0 {
			sh := uint32(windows.STD_ERROR_HANDLE)
			setStdHandle.Call(uintptr(sh), uintptr(oldStderr))
			panicFile.Close()
			os.Remove(panicFile.Name())
		}
	}
}
