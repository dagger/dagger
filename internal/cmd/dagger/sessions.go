package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
)

var sessionsCmd = newSessionsCommand()

const attachmentWaitMessage = "Waiting for the previous client connection to be declared unavailable..."

func newSessionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Manage detachable sessions (experimental)",
	}
	cmd.AddCommand(
		newSessionsListCommand(),
		newSessionsInspectCommand(),
		newSessionsAttachCommand(),
		newSessionsTerminateCommand(),
	)
	return cmd
}

func newSessionsListCommand() *cobra.Command {
	var outputJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List detachable sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSessionControlClient(cmd.Context(), func(ctx context.Context, control *client.ControlClient) error {
				sessions, err := control.ListSessions(ctx)
				if err != nil {
					return err
				}
				if outputJSON {
					return writeSessionsJSON(cmd.OutOrStdout(), sessions)
				}
				return writeSessionsTable(cmd.OutOrStdout(), sessions)
			})
		},
	}
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Present result as JSON")
	return cmd
}

func newSessionsInspectCommand() *cobra.Command {
	var outputJSON bool
	cmd := &cobra.Command{
		Use:   "inspect SESSION",
		Short: "Inspect a detachable session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSessionCLIArg(cmd, args[0]); err != nil {
				return err
			}
			return withSessionControlClient(cmd.Context(), func(ctx context.Context, control *client.ControlClient) error {
				descriptor, err := control.InspectSession(ctx, args[0])
				if err != nil {
					return err
				}
				if outputJSON {
					return writeSessionsJSON(cmd.OutOrStdout(), descriptor)
				}
				return writeSessionInspect(cmd.OutOrStdout(), descriptor)
			})
		},
	}
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Present result as JSON")
	return cmd
}

func newSessionsAttachCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "attach SESSION",
		Short:       "Attach to a detachable session",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{showFinalProgressKey: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			if err := validateSessionCLIArg(cmd, sessionID); err != nil {
				return err
			}
			return attachDetachableSession(cmd, sessionID)
		},
	}
	return cmd
}

func newSessionsTerminateCommand() *cobra.Command {
	var outputJSON bool
	cmd := &cobra.Command{
		Use:   "terminate SESSION",
		Short: "Terminate a detachable session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			if err := validateSessionCLIArg(cmd, sessionID); err != nil {
				return err
			}
			return withSessionControlClient(cmd.Context(), func(ctx context.Context, control *client.ControlClient) error {
				terminateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 70*time.Second)
				defer cancel()
				if err := control.TerminateSession(terminateCtx, sessionID); err != nil {
					if terminateCtx.Err() != nil {
						return fmt.Errorf("termination started but completion was not observed: %w", err)
					}
					return err
				}
				if outputJSON {
					return writeSessionsJSON(cmd.OutOrStdout(), struct {
						SessionID  string `json:"session_id"`
						Terminated bool   `json:"terminated"`
					}{SessionID: sessionID, Terminated: true})
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Terminated %s\n", sessionID)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Present result as JSON")
	return cmd
}

func withSessionControlClient(ctx context.Context, fn func(context.Context, *client.ControlClient) error) (rerr error) {
	return Frontend.Run(ctx, opts, func(ctx context.Context) (_ cleanups.CleanupF, rerr error) {
		var cleanup cleanups.Cleanups
		ctx, cleanupTelemetry := initEngineTelemetry(ctx)
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			if opts.Debug {
				slog.Error("failed to emit telemetry", "error", err)
			}
			Frontend.SetTelemetryError(err)
		}))
		cleanup.Add("close telemetry", func() error {
			cleanupTelemetry(rerr)
			return nil
		})
		params, err := finalizeEngineParams(ctx, client.Params{})
		if err != nil {
			return cleanup.Run, err
		}
		control, err := client.ConnectControl(ctx, params)
		if err != nil {
			return cleanup.Run, err
		}
		cleanup.Add("close session control client", control.Close)
		return cleanup.Run, fn(ctx, control)
	})
}

func attachDetachableSession(cmd *cobra.Command, sessionID string) (rerr error) {
	return Frontend.Run(cmd.Context(), opts, func(ctx context.Context) (_ cleanups.CleanupF, rerr error) {
		var cleanup cleanups.Cleanups
		ctx, cleanupTelemetry := initEngineTelemetry(ctx)
		// The creator query belongs to the initiating CLI's trace, while this
		// command has a new local root. Show both roots so replayed creator
		// progress is visible instead of remaining behind the local zoom.
		Frontend.RevealAllSpans()
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			if opts.Debug {
				slog.Error("failed to emit telemetry", "error", err)
			}
			Frontend.SetTelemetryError(err)
		}))
		cleanup.Add("close telemetry", func() error {
			cleanupTelemetry(rerr)
			return nil
		})

		baseParams, err := finalizeEngineParams(ctx, client.Params{})
		if err != nil {
			return cleanup.Run, err
		}
		control, err := client.ConnectControl(ctx, baseParams)
		if err != nil {
			return cleanup.Run, err
		}
		descriptor, inspectErr := control.InspectSession(ctx, sessionID)
		closeErr := control.Close()
		if inspectErr != nil {
			return cleanup.Run, errors.Join(inspectErr, closeErr)
		}
		if closeErr != nil {
			return cleanup.Run, closeErr
		}
		if descriptor.Query == nil {
			return cleanup.Run, errors.New("session has no primary query")
		}

		attachParams := baseParams
		attachParams.AttachSessionID = sessionID
		attachParams.OnAttachWait = printAttachmentWaitMessage
		observer, err := client.Connect(ctx, attachParams)
		if err != nil {
			return cleanup.Run, err
		}
		cleanup.Add("close session attachment", observer.Close)

		telemetryDone := make(chan error, 1)
		go func() { telemetryDone <- observer.WaitTelemetry() }()
		select {
		case err := <-telemetryDone:
			if err != nil {
				return cleanup.Run, err
			}
		case <-ctx.Done():
			closeErr := observer.Close()
			telemetryErr := <-telemetryDone
			return cleanup.Run, idtui.ExitError{
				OriginalCode: 130,
				Original:     errors.Join(context.Cause(ctx), closeErr, telemetryErr),
			}
		}

		query, err := observer.InspectPrimaryQuery(ctx)
		if err != nil {
			var protocolErr *client.SessionProtocolError
			if errors.As(err, &protocolErr) && protocolErr.Code == engine.SessionErrorSessionNotFound {
				return cleanup.Run, errors.New("detachable session was terminated")
			}
			return cleanup.Run, err
		}
		result, err := observer.PrimaryQueryResult(ctx)
		if err != nil {
			return cleanup.Run, err
		}
		value, err := extractDetachedResult(result.Body, query.Presentation)
		if err != nil {
			return cleanup.Run, err
		}
		if err := formatDetachedResult(cmd.OutOrStdout(), value, query.Presentation); err != nil {
			return cleanup.Run, err
		}
		switch query.Status {
		case engine.SessionQueryStateSucceeded:
			return cleanup.Run, nil
		case engine.SessionQueryStateCanceled:
			return cleanup.Run, errors.New("detached query was canceled")
		case engine.SessionQueryStateResultDiscarded:
			return cleanup.Run, errors.New("detached query result was discarded")
		default:
			if query.Error != "" {
				return cleanup.Run, errors.New(query.Error)
			}
			return cleanup.Run, fmt.Errorf("detached query finished with status %s", query.Status)
		}
	})
}

func printAttachmentWaitMessage() {
	if progress == "tty" {
		if printer, ok := Frontend.(interface{ PrintAbove(string) }); ok {
			printer.PrintAbove(attachmentWaitMessage)
			return
		}
	}
	// Command stderr is redirected into root-span telemetry while a frontend is
	// running. Plain/report final rendering then replays that span output, which
	// would display a one-shot status twice. The retained process stderr is the
	// frontend's own writer and emits this transient line exactly once.
	fmt.Fprintln(stderr, attachmentWaitMessage)
}

func validateSessionCLIArg(cmd *cobra.Command, sessionID string) error {
	if err := engine.ValidateSessionID(sessionID); err != nil {
		usageErr := fmt.Errorf("malformed session ID %q: %w", sessionID, err)
		cmd.PrintErrln(cmd.ErrPrefix(), usageErr)
		cmd.PrintErrf("Run '%s --help' for usage.\n", cmd.CommandPath())
		return idtui.ExitError{OriginalCode: 2, Original: usageErr}
	}
	return nil
}

func writeSessionsJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "    ")
	return encoder.Encode(value)
}

func writeSessionsTable(w io.Writer, sessions []engine.SessionDescriptor) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintln(tw, "ID\tSTATE\tQUERY\tSTATUS\tCREATED\tATTACHED")
	for _, descriptor := range sessions {
		queryID, status := "-", "-"
		if descriptor.Query != nil {
			queryID, status = descriptor.Query.ID, string(descriptor.Query.Status)
		}
		attached := "-"
		if descriptor.Attachment != nil {
			attached = descriptor.Attachment.ID
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			descriptor.ID, descriptor.State, queryID, status,
			descriptor.CreatedAt.UTC().Format(time.RFC3339), attached)
	}
	return tw.Flush()
}

func writeSessionInspect(w io.Writer, descriptor engine.SessionDescriptor) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	queryID, status := "-", "-"
	if descriptor.Query != nil {
		queryID, status = descriptor.Query.ID, string(descriptor.Query.Status)
	}
	attached := "-"
	if descriptor.Attachment != nil {
		attached = descriptor.Attachment.ID
	}
	rows := [][2]string{
		{"ID", descriptor.ID}, {"State", string(descriptor.State)},
		{"Query", queryID}, {"Status", status},
		{"Created", descriptor.CreatedAt.UTC().Format(time.RFC3339)}, {"Attached", attached},
	}
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\n", row[0], strings.TrimSpace(row[1]))
	}
	return tw.Flush()
}
