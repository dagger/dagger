package daggercmd

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"dagger.io/dagger"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/client"
)

var (
	// sessionFlag is the global --session flag.
	sessionFlag string

	sessionsStopClient string
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List, inspect, attach to and stop the sessions on the engine (experimental)",
	Long: `List, inspect, attach to and stop the sessions on the engine.

A session started with 'dagger call --detach' or 'dagger up --detach' keeps
running after the command returns, until it is stopped. Run another command in
a session with the global --session flag.`,
	Annotations: map[string]string{
		"experimental": "true",
	},
	Args: cobra.NoArgs,
	RunE: runSessionsList,
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the sessions on the engine",
	Args:  cobra.NoArgs,
	RunE:  runSessionsList,
}

var sessionsInspectCmd = &cobra.Command{
	Use:   "inspect SESSION",
	Short: "Show a session's clients and what each provides",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionsInspect,
}

var sessionsAttachCmd = &cobra.Command{
	Use:   "attach SESSION",
	Short: "Follow a session's work from its start, and provide a terminal and prompts for it",
	Long: `Follow a session's work from its start, and provide a terminal and prompts for it.

Attaching replays the session's progress and output, then follows it live
until the session ends. Work in the session that opens a terminal or asks a
question gets this terminal. Press Ctrl-C to leave; a detached session keeps
running.`,
	Args: cobra.ExactArgs(1),
	Annotations: map[string]string{
		showFinalProgressKey: "true",
	},
	RunE: runSessionsAttach,
}

var sessionsStopCmd = &cobra.Command{
	Use:   "stop SESSION",
	Short: "End a session and everything running in it, or disconnect one of its clients",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionsStop,
}

func init() {
	sessionsStopCmd.Flags().StringVar(&sessionsStopClient, "client", "", "Disconnect only this client of the session")
	sessionsCmd.AddCommand(sessionsListCmd, sessionsInspectCmd, sessionsAttachCmd, sessionsStopCmd)
}

type sessionsListing struct {
	Engine struct {
		Sessions []sessionDescription
	}
}

type sessionDescription struct {
	SessionID string
	Detached  bool
	CreatedAt string
	Clients   []sessionClientDescription
}

type sessionClientDescription struct {
	ClientID   string
	Hostname   string
	PID        *int
	Command    *string
	Background bool
	Connected  bool
	Provides   []string
	Workspace  *string
	Forwards   []struct {
		Port     int
		Protocol string
	}
}

func listSessions(ctx context.Context, dag *dagger.Client) ([]sessionDescription, error) {
	var res sessionsListing
	err := dag.Do(ctx, &dagger.Request{
		Query: `{ engine { sessions {
			sessionID detached createdAt
			clients {
				clientID hostname pid command background connected provides workspace
				forwards { port protocol }
			}
		} } }`,
	}, &dagger.Response{Data: &res})
	return res.Engine.Sessions, err
}

func runSessionsList(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
		sessions, err := listSessions(ctx, engineClient.Dagger())
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(tw, "SESSION\tDETACHED\tAGE\tCLIENTS\tCOMMAND")
		for _, sess := range sessions {
			// Leave out the session this command runs in.
			if sess.SessionID == engineClient.SessionID {
				continue
			}
			connected := 0
			for _, c := range sess.Clients {
				if c.Connected {
					connected++
				}
			}
			command := ""
			if len(sess.Clients) > 0 && sess.Clients[0].Command != nil {
				command = *sess.Clients[0].Command
			}
			fmt.Fprintf(tw, "%s\t%t\t%s\t%d\t%s\n", sess.SessionID, sess.Detached, sessionAge(sess.CreatedAt), connected, command)
		}
		return tw.Flush()
	})
}

func sessionAge(createdAt string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return createdAt
	}
	return time.Since(t).Round(time.Second).String()
}

func runSessionsInspect(cmd *cobra.Command, args []string) error {
	id := args[0]
	return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
		sessions, err := listSessions(ctx, engineClient.Dagger())
		if err != nil {
			return err
		}
		for _, sess := range sessions {
			if sess.SessionID == id {
				return printSession(cmd.OutOrStdout(), sess)
			}
		}
		return fmt.Errorf("session %q not found", id)
	})
}

func printSession(w io.Writer, sess sessionDescription) error {
	kind := "attached"
	if sess.Detached {
		kind = "detached"
	}
	fmt.Fprintf(w, "Session %s, %s, started %s ago\n\n", sess.SessionID, kind, sessionAge(sess.CreatedAt))
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "CLIENT\tHOST\tPID\tMODE\tCONNECTED\tPROVIDES\tFORWARDS\tWORKSPACE\tCOMMAND")
	for _, c := range sess.Clients {
		pid := ""
		if c.PID != nil {
			pid = strconv.Itoa(*c.PID)
		}
		mode := "foreground"
		if c.Background {
			mode = "background"
		}
		var forwards []string
		for _, f := range c.Forwards {
			forwards = append(forwards, fmt.Sprintf("%d/%s", f.Port, strings.ToLower(f.Protocol)))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\t%s\t%s\t%s\n",
			c.ClientID, c.Hostname, pid, mode, c.Connected,
			strings.Join(c.Provides, ","), strings.Join(forwards, ","),
			deref(c.Workspace), deref(c.Command))
	}
	return tw.Flush()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func runSessionsAttach(cmd *cobra.Command, args []string) error {
	// Show the work of the session's commands, each its own trace, rather
	// than this command's.
	previous := opts.RootFilter
	opts.RootFilter = sessionRootSpans
	defer func() { opts.RootFilter = previous }()
	params := client.Params{
		SessionID:           args[0],
		JoinExistingSession: true,
		SessionTelemetry:    true,
	}
	return withEngine(cmd.Context(), params, func(ctx context.Context, engineClient *client.Client) error {
		done := make(chan error, 1)
		go func() { done <- engineClient.WaitTelemetry() }()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return nil
		}
	})
}

// sessionRootSpans selects the root spans of the session's traces, leaving
// out this command's own.
func sessionRootSpans(db *dagui.DB, _ *dagui.Span) []*dagui.Span {
	var roots []*dagui.Span
	for _, span := range db.Spans.Order {
		if span.ParentSpan == nil && span.ID != db.PrimarySpan {
			roots = append(roots, span)
		}
	}
	return roots
}

func runSessionsStop(cmd *cobra.Command, args []string) error {
	id := args[0]
	return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		session := dag.Engine().Session(id)
		if sessionsStopClient != "" {
			if err := session.Client(sessionsStopClient).Close(ctx); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Disconnected client %s from session %s\n", sessionsStopClient, id)
			return nil
		}
		if err := session.Stop(ctx); err != nil {
			return err
		}
		// Stop returns once teardown is scheduled; wait until it is done.
		for {
			sessions, err := listSessions(ctx, dag)
			if err != nil {
				return err
			}
			found := false
			for _, sess := range sessions {
				found = found || sess.SessionID == id
			}
			if !found {
				break
			}
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Stopped session %s\n", id)
		return nil
	})
}
