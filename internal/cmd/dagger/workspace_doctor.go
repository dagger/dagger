package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger.io/dagger"
	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var workspaceDoctorCmd = newWorkspaceDoctorCmd(false)
var doctorAliasCmd = newWorkspaceDoctorCmd(true)

func newWorkspaceDoctorCmd(hidden bool) *cobra.Command {
	return &cobra.Command{
		Use:    "doctor",
		Short:  "Check whether your workspace is ready to run Dagger",
		Hidden: hidden,
		Long: `Check workspace configuration, lockfile, engine connectivity, Cloud authentication,
module loading, and module settings.

Missing configuration or lockfiles, legacy configuration fields, and Cloud
authentication problems are warnings.
Invalid files, unavailable engines, module loading failures, and invalid settings
cause a nonzero exit status. Object settings are resolved using their configured
addresses, which may invoke module functions. Loading modules can populate dagger.lock.`,
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			showFinalProgressKey: "true",
		},
		RunE: runWorkspaceDoctor,
	}
}

// doctorCloudAuthKey supplies the already checked credentials to engine setup.
type doctorCloudAuthKey struct{}

type doctorStatus string

const (
	doctorPass doctorStatus = "PASS"
	doctorWarn doctorStatus = "WARN"
	doctorFail doctorStatus = "FAIL"
)

type doctorResult struct {
	Name   string
	Status doctorStatus
	Detail string
}

// doctorReport renders each diagnostic as a revealed span and collects the
// failures that determine the exit status.
type doctorReport struct {
	results []doctorResult
	errs    []error
}

// check runs one diagnostic in its own span, so the TUI shows it while it
// runs. fn returns a detail for a passing diagnostic. A warning marks the
// span skipped and names the reason; a failure fails the span.
func (r *doctorReport) check(ctx context.Context, name string, warning bool, fn func(context.Context) (string, error), spanOpts ...trace.SpanStartOption) error {
	ctx, span := Tracer().Start(ctx, name, append([]trace.SpanStartOption{telemetry.Reveal()}, spanOpts...)...)
	detail, err := fn(ctx)
	result := doctorResult{Name: name, Status: doctorPass, Detail: detail}
	var spanErr error
	switch {
	case err == nil:
		if detail != "" {
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
			fmt.Fprintln(stdio.Stdout, detail)
			stdio.Close()
		}
	case warning:
		// The TUI has no warning status, and a successful span hides its logs.
		// Show the warning as a skipped row that names the reason.
		result.Status, result.Detail = doctorWarn, err.Error()
		span.SetName(name + ": " + result.Detail)
		span.SetAttributes(attribute.Bool(telemetry.CanceledAttr, true))
	default:
		result.Status, result.Detail = doctorFail, err.Error()
		r.errs = append(r.errs, fmt.Errorf("%s: %w", name, err))
		spanErr = err
	}
	r.results = append(r.results, result)
	telemetry.EndWithCause(span, &spanErr)
	return err
}

// result reports a diagnostic that has already been evaluated.
func (r *doctorReport) result(ctx context.Context, name, detail string, err error, warning bool) {
	_ = r.check(ctx, name, warning, func(context.Context) (string, error) {
		return detail, err
	})
}

func runWorkspaceDoctor(cmd *cobra.Command, _ []string) error {
	params := client.Params{LoadWorkspaceModules: true}
	return withLazySessions(cmd.Context(), params, nil, func(ctx context.Context, connect func(context.Context) (*client.Client, func(), error)) error {
		report := &doctorReport{}
		runDoctorChecks(ctx, report, connect)
		if len(report.errs) > 0 {
			// Each failure is already shown in its diagnostic's span.
			return idtui.ExitError{OriginalCode: 1, Original: errors.Join(report.errs...)}
		}
		return nil
	})
}

func runDoctorChecks(ctx context.Context, report *doctorReport, connect func(context.Context) (*client.Client, func(), error)) {
	ref := workspaceRef
	if ref == "" {
		ref = sessionWorkspace
	}
	remote := isObviouslyRemoteWorkspaceRef(ref)
	if !remote {
		if ref == "" {
			ref = "."
		}
		cwd, err := filepath.Abs(ref)
		var ws *workspacepkg.Workspace
		if err == nil {
			var info os.FileInfo
			info, err = os.Stat(cwd)
			if err == nil && !info.IsDir() {
				err = fmt.Errorf("workspace %q is not a directory", ref)
			}
		}
		if err == nil {
			ws, err = workspacepkg.Detect(ctx, localPathExists, cwd)
		}
		if err != nil {
			report.result(ctx, "Workspace", "", err, false)
		} else {
			doctorWorkspaceFiles(ctx, report, ws, os.ReadFile)
		}
	}

	cloudAuth, authErr := cloudCLI.cloudAuthWithLogin(ctx, false)
	report.result(ctx, "Cloud login", "credentials available", authErr, true)

	// Reuse the diagnostic auth result so malformed Cloud credentials do not
	// prevent connecting to a local engine. A Cloud engine still requires auth.
	// Connect under the command span, as other commands do, so the session's
	// own spans are not nested in a diagnostic.
	sess, closeSession, err := connect(context.WithValue(ctx, doctorCloudAuthKey{}, cloudAuth))
	report.result(ctx, "Engine", "connected", err, false)
	if err != nil {
		return
	}
	defer closeSession()

	ws := sess.Dagger().CurrentWorkspace()
	if remote {
		doctorRemoteWorkspaceFiles(ctx, report, ws)
	}
	err = report.check(ctx, "Workspace loading", false, func(ctx context.Context) (string, error) {
		_, err := ws.ConfigRead(ctx)
		return "selected configuration and environment loaded", err
	}, telemetry.Encapsulate())
	if err == nil {
		doctorWorkspaceModules(ctx, report, sess.Dagger())
	}
}

// Read both files independently: a malformed config must not hide lock errors.
func doctorWorkspaceFiles(ctx context.Context, report *doctorReport, ws *workspacepkg.Workspace, readFile func(string) ([]byte, error)) {
	if ws == nil {
		report.result(ctx, "Workspace config", "", errors.New("no workspace configuration found"), true)
		report.result(ctx, "Lockfile", "", errors.New("no workspace lockfile found"), true)
		return
	}
	if ws.ConfigFile == "" {
		report.result(ctx, "Workspace config", "", errors.New("no dagger.toml selected"), true)
	} else {
		data, err := readFile(filepath.Join(ws.Root, ws.ConfigFile))
		var warnings []string
		if err == nil {
			_, err = workspacepkg.ParseConfigAt(ctx, data, filepath.Dir(ws.ConfigFile))
		}
		if err == nil {
			// ParseConfigAt only logs legacy fields, and a passing row hides logs.
			warnings, _ = workspacepkg.CheckConfigFields(data, filepath.ToSlash(ws.ConfigFile))
			if len(warnings) > 0 {
				err = errors.New(strings.Join(warnings, "; "))
			}
		}
		report.result(ctx, "Workspace config", ws.ConfigFile+" is loadable", err, len(warnings) > 0)
	}
	lockPath := filepath.Join(ws.Root, ws.LockFile)
	data, err := readFile(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		lockPath = workspacepkg.LegacyLockFilePathForCanonical(lockPath)
		data, err = readFile(lockPath)
	}
	if errors.Is(err, os.ErrNotExist) {
		report.result(ctx, "Lockfile", "", errors.New("no dagger.lock found"), true)
		return
	}
	if err == nil {
		_, err = workspacepkg.ParseLock(data)
	}
	report.result(ctx, "Lockfile", lockPath+" is loadable", err, false)
}

func doctorRemoteWorkspaceFiles(ctx context.Context, report *doctorReport, ws *dagger.Workspace) {
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		report.result(ctx, "Workspace files", "", err, false)
		return
	}
	doctorWorkspaceFilesInRoot(ctx, report, cwd, func(dir string) ([]string, error) {
		return ws.Directory("/"+dir, dagger.WorkspaceDirectoryOpts{Include: []string{"*"}, Exclude: []string{"*/*"}}).Entries(ctx)
	}, func(name string) ([]byte, error) {
		contents, err := ws.File("/" + filepath.ToSlash(name)).Contents(ctx)
		return []byte(contents), err
	})
}

func doctorWorkspaceFilesInRoot(ctx context.Context, report *doctorReport, cwd string, listDir func(string) ([]string, error), readFile func(string) ([]byte, error)) {
	entries := map[string][]string{}
	var exists workspacepkg.PathExistsFunc
	exists = func(_ context.Context, name string) (string, bool, error) {
		dir := path.Dir(name)
		if dir != "." {
			_, found, err := exists(ctx, dir)
			if err != nil || !found {
				return dir, false, err
			}
		}
		names, ok := entries[dir]
		if !ok {
			var err error
			names, err = listDir(dir)
			if err != nil {
				return "", false, err
			}
			entries[dir] = names
		}
		return dir, slices.Contains(names, path.Base(name)) || slices.Contains(names, path.Base(name)+"/"), nil
	}
	detected, err := workspacepkg.DetectInRoot(ctx, exists, path.Clean(strings.TrimPrefix(cwd, "/")), ".")
	if err != nil {
		report.result(ctx, "Workspace files", "", err, false)
		return
	}
	doctorWorkspaceFiles(ctx, report, detected, func(name string) ([]byte, error) {
		_, found, err := exists(ctx, name)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, os.ErrNotExist
		}
		return readFile(name)
	})
}

func doctorWorkspaceModules(ctx context.Context, report *doctorReport, dag *dagger.Client) {
	type diagnostic struct {
		Name  string
		Check string
		Error string
	}
	var diagnostics []diagnostic
	// Module loading failures are reported per module below, so keep the
	// engine's loading spans out of the way.
	err := report.check(ctx, "Workspace modules", false, func(ctx context.Context) (string, error) {
		var response struct {
			CurrentWorkspace struct {
				Doctor string `json:"__doctor"`
			}
		}
		err := dag.Do(ctx, &dagger.Request{Query: `query { currentWorkspace { __doctor } }`}, &dagger.Response{Data: &response})
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal([]byte(response.CurrentWorkspace.Doctor), &diagnostics); err != nil {
			return "", err
		}
		if len(diagnostics) == 0 {
			return "no modules configured", nil
		}
		return "", nil
	}, telemetry.Encapsulate())
	if err != nil {
		return
	}
	for _, diagnostic := range diagnostics {
		var err error
		if diagnostic.Error != "" {
			err = errors.New(diagnostic.Error)
		}
		report.result(ctx, fmt.Sprintf("%s %q", diagnostic.Check, diagnostic.Name), "valid", err, false)
	}
}
