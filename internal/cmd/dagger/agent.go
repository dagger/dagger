package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/charmbracelet/huh"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

var agentListMode bool
var agentResume agentResumeFlag
var agentTrace string
var agentFocus string

var agentCmd = &cobra.Command{
	Use:   "agent [FILTERS] [OPTIONS]",
	Short: "Compose your installed agent modules and drop into an interactive prompt.",
	Args:  cobra.ArbitraryArgs,
	Annotations: map[string]string{
		// Drop into the same interactive prompt mode as `dagger shell`, so keep
		// completed conversation items in scrollback rather than GC'ing them
		// (verbosity 0 prunes completed spans after GCThreshold).
		showFinalProgressKey: "true",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		traceID, listArchives, args, err := resolveResumeFlags(
			agentResume, cmd.Flags().Changed("resume"),
			agentTrace, cmd.Flags().Changed("trace"),
			args)
		if err != nil {
			return err
		}
		// Refuse the combinations that have no meaning before any engine work
		// happens (hack/designs/resume-from-trace.md §5.4).
		if err := validateAgentTraceFlags(traceID, args); err != nil {
			return err
		}
		if err := validateArchiveFlags(traceID, agentFocus, listArchives, agentListMode, args); err != nil {
			return err
		}
		// A bare -r offers a picker whenever the frontend can prompt, then
		// resumes the chosen session; otherwise it prints the table.
		pickArchive := listArchives && canPromptForInit(progress, stdinIsTTY, false)
		// The prompt is about to use the LLM, so renew an expired subscription
		// login up front. The llmconfig:// resolver serves the renewed token
		// on the engine's first credential lookup.
		if !agentListMode && (!listArchives || pickArchive) {
			if err := llmconfig.RefreshOAuthTokensIfNeeded(cmd.Context()); err != nil {
				slog.Warn("failed to refresh LLM OAuth tokens", "error", err)
			}
		}
		params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, args)
		if err != nil {
			return err
		}
		// Set once the prompt exits with agents behind it and an engine archive
		// or Cloud can serve the trace; read after the TUI has torn down.
		// Atomic because a second Ctrl+D exits the frontend without waiting
		// for the run to return.
		var resumeTrace atomic.Pointer[string]
		err = withEngine(
			cmd.Context(), params,
			func(ctx context.Context, engineClient *client.Client) error {
				source := archive.NewClient(client.EngineConn(engineClient))
				if listArchives {
					if !pickArchive {
						return listAgentArchives(ctx, source, cmd.OutOrStdout())
					}
					picked, err := pickAgentArchive(ctx, source, chooseArchiveWithFrontend)
					if err != nil || picked == "" {
						return err
					}
					traceID = picked
				}
				dag := engineClient.Dagger()
				if agentListMode {
					return listAgents(ctx, dag, args, cmd)
				}
				// Compose all selected agents onto a workspace-bound LLM, capturing
				// a stable baseline when possible, then open the prompt. A module
				// function returning LLM already lands in prompt mode today.
				//
				// Trace restore initializes only inert client plumbing. Its archived
				// anchors, not a destination base LLM, own the provider and workspace.
				var llmID string
				var err error
				if traceID == "" {
					llmID, err = composeAgents(ctx, dag, args, cmd)
				}
				if err != nil {
					return err
				}
				restore := traceRestore{
					source:  source,
					traceID: traceID,
					agent:   agentFocus,
				}
				return startInteractivePromptModeWithResume(ctx, dag, llmID, interactivePromptModeOpts{
					restore:              restore,
					generateSessionTitle: true,
					exitedResumable: func(traceID string) {
						if resumeServable(ctx, source, traceID, cloudTraceConfigured) {
							resumeTrace.Store(&traceID)
						}
					},
				})
			},
		)
		if resumed := resumeTrace.Load(); resumed != nil {
			printResumeHint(cmd.ErrOrStderr(), *resumed)
		}
		return err
	},
}

// archiveLookup is the slice of the archive client resumeServable needs.
type archiveLookup interface {
	Unsealed(context.Context, string) (archive.UnsealedArchive, error)
}

// resumeServable reports whether `dagger agent --resume traceID` has a source
// to restore from, so the exit hint is not printed when nothing can serve it.
// The connected engine is asked first: a manifest in any state (the live
// session's archive is still active) means it retains the trace. Otherwise the
// run must be publishing to Dagger Cloud, the same condition that yields a
// Cloud trace URL.
func resumeServable(ctx context.Context, source archiveLookup, traceID string, cloudConfigured func(context.Context) bool) bool {
	// The prompt may have exited through a cancellation; a bounded lookup on
	// the still-connected engine must not delay exit.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := source.Unsealed(ctx, traceID)
	var requestErr *archive.RequestError
	if err == nil || (errors.As(err, &requestErr) && requestErr.Kind == archive.ErrorState) {
		return true
	}
	return cloudConfigured(ctx)
}

// cloudTraceConfigured reports whether this run publishes its trace to Dagger
// Cloud, i.e. whether the CLI has a Cloud trace URL for it.
func cloudTraceConfigured(ctx context.Context) bool {
	_, _, ok := enginetel.URLForTrace(ctx)
	return ok
}

// printResumeHint tells the user how to come back to the session they just
// left, spelling out --resume since -r alone is easy to forget.
func printResumeHint(w io.Writer, traceID string) {
	out := termenv.NewOutput(w)
	fmt.Fprintln(w, out.String("To resume this session, run:").Bold())
	fmt.Fprintf(w, "  dagger agent --resume %s\n", traceID)
}

// agentResumeFlag is the -r/--resume value: the trace to restore, or the
// reserved word "list" that a bare -r resolves to (via NoOptDefVal). A custom
// pflag.Value keeps the help readable — `--resume trace[=list]` — since pflag
// renders a custom type's NoOptDefVal unquoted after its Type() name. Trace IDs
// are hex, so the keyword cannot shadow one.
type agentResumeFlag string

const agentResumeList agentResumeFlag = "list"

func (f *agentResumeFlag) String() string { return string(*f) }

func (f *agentResumeFlag) Set(value string) error {
	*f = agentResumeFlag(value)
	return nil
}

func (f *agentResumeFlag) Type() string { return "trace" }

// resolveResumeFlags folds -r/--resume and its deprecated --trace alias into
// the trace to restore, or a request to list archives, returning the
// remaining positional arguments.
func resolveResumeFlags(resume agentResumeFlag, resumeSet bool, traceAlias string, traceAliasSet bool, args []string) (traceID string, listArchives bool, rest []string, err error) {
	switch {
	case resumeSet && traceAliasSet:
		return "", false, nil, errors.New("--trace is a deprecated alias of -r/--resume; pass only -r")
	case traceAliasSet:
		if traceAlias == "" {
			return "", false, nil, errors.New("--trace requires a trace ID")
		}
		return traceAlias, false, args, nil
	case !resumeSet:
		return "", false, args, nil
	case resume == "":
		return "", false, nil, errors.New("-r/--resume requires a trace ID, or no value to list archives")
	case resume != agentResumeList:
		return string(resume), false, args, nil
	}
	// A bare -r takes no value, so `-r <id>` parses the ID as a positional
	// argument. Agent names are never trace IDs, so accept that spelling too.
	if len(args) == 1 {
		if _, err := trace.TraceIDFromHex(args[0]); err == nil {
			return args[0], false, nil, nil
		}
	}
	return "", true, args, nil
}

func init() {
	registerCommandArtifactFlags(agentCmd)
	agentCmd.Flags().BoolVarP(&agentListMode, "list", "l", false, "List available agents")
	agentCmd.Flags().VarP(&agentResume, "resume", "r",
		"Restore agents from a past session's trace: a retained engine archive, falling back to Dagger Cloud. With no trace ID, pick a retained session to resume (or list them when not interactive)")
	// A bare -r resolves to the list keyword; -r <trace-id> and -r=<trace-id>
	// both restore (resolveResumeFlags recognizes the space-separated form).
	agentCmd.Flags().Lookup("resume").NoOptDefVal = string(agentResumeList)
	agentCmd.Flags().StringVar(&agentTrace, "trace", "", "Restore agents from a past session's trace")
	_ = agentCmd.Flags().MarkDeprecated("trace", "use -r/--resume <trace-id> instead")
	agentCmd.Flags().StringVar(&agentFocus, "agent", "",
		"With -r, focus this restored agent (runtime handle or name) instead of the top-level one")
}

func validateArchiveFlags(traceID, focus string, listArchives, listAgents bool, args []string) error {
	if listArchives {
		if listAgents || len(args) != 0 || focus != "" {
			return fmt.Errorf("-r without a trace ID picks a retained session; do not combine it with agent names, --list, or --agent")
		}
		return nil
	}
	if traceID != "" && listAgents {
		return fmt.Errorf("-r/--resume cannot be combined with --list")
	}
	if traceID == "" && focus != "" {
		return fmt.Errorf("--agent requires -r/--resume <trace-id>")
	}
	return nil
}

type agentArchiveLister interface {
	ListAll(context.Context, archive.ListOptions) ([]archive.Manifest, error)
}

// Listing only reads archive metadata: no bootstrap, module composition,
// provider lookup, or runtime restoration are needed.
func listAgentArchives(ctx context.Context, source agentArchiveLister, out io.Writer) error {
	manifests, err := source.ListAll(ctx, archive.ListOptions{})
	if err != nil {
		return err
	}
	sortArchivesRecentFirst(manifests)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "TRACE\tSTATE\tSTARTED\tTITLE"); err != nil {
		return err
	}
	for _, m := range manifests {
		// Quoting prevents user-provided titles from injecting terminal controls
		// or breaking a metadata row into multiple lines.
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%q\n", m.TraceID, m.State, m.StartedAt.UTC().Format(time.RFC3339), m.Title); err != nil {
			return err
		}
	}
	return w.Flush()
}

func sortArchivesRecentFirst(manifests []archive.Manifest) {
	slices.SortStableFunc(manifests, func(a, b archive.Manifest) int {
		return b.StartedAt.Compare(a.StartedAt)
	})
}

// resumableArchives keeps the sessions a restore can read, newest first:
// sealed ones, and unsealed ones restored best-effort. Active and finalizing
// archives belong to sessions still running.
func resumableArchives(manifests []archive.Manifest) []archive.Manifest {
	resumable := slices.DeleteFunc(slices.Clone(manifests), func(m archive.Manifest) bool {
		return m.State != archive.StateClosed && !m.State.Unsealed()
	})
	sortArchivesRecentFirst(resumable)
	return resumable
}

// archiveChooser presents the options and returns the chosen trace ID, or ""
// when the user dismissed the picker.
type archiveChooser func(context.Context, []huh.Option[string]) (string, error)

// pickAgentArchive lets the user choose a retained session to resume.
func pickAgentArchive(ctx context.Context, source agentArchiveLister, choose archiveChooser) (string, error) {
	manifests, err := source.ListAll(ctx, archive.ListOptions{})
	if err != nil {
		return "", err
	}
	resumable := resumableArchives(manifests)
	if len(resumable) == 0 {
		return "", errors.New("the connected engine has no agent sessions to resume; to resume one from Dagger Cloud, run: dagger agent -r <trace-id>")
	}
	options := make([]huh.Option[string], len(resumable))
	for i, m := range resumable {
		options[i] = huh.NewOption(archiveLabel(m, time.Local), m.TraceID)
	}
	return choose(ctx, options)
}

func chooseArchiveWithFrontend(ctx context.Context, options []huh.Option[string]) (string, error) {
	var selected string
	form := idtui.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Resume an agent session").
			Height(min(len(options)+2, 12)).
			Filtering(true).
			Options(options...).
			Value(&selected),
	))
	if err := Frontend.HandleForm(ctx, form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}
		return "", err
	}
	return selected, nil
}

const archiveLabelTitleMax = 60

// archiveLabel renders one picker row: start time, title, and the trace ID so
// the filter can match an ID prefix too.
func archiveLabel(m archive.Manifest, loc *time.Location) string {
	// Titles are user-influenced: flatten control characters and newlines so
	// a title can neither emit terminal controls nor break the row.
	title := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, m.Title)), " ")
	if runes := []rune(title); len(runes) > archiveLabelTitleMax {
		title = string(runes[:archiveLabelTitleMax-1]) + "…"
	}
	if title == "" {
		title = "(untitled)"
	}
	if m.State.Unsealed() {
		title += " [" + string(m.State) + "]"
	}
	return fmt.Sprintf("%s  %s  %s", m.StartedAt.In(loc).Format("2006-01-02 15:04"), title, m.TraceID)
}

func composeAgents(ctx context.Context, dag *dagger.Client, include []string, cmd *cobra.Command) (string, error) {
	workspace, err := snapshotWorkspace(ctx, dag)
	if err != nil {
		return "", err
	}
	all, err := commandArtifactsWithFlags(ctx, dag, workspace, cmd, include, true)
	if err != nil {
		return "", err
	}
	selection := all.FilterTypes([]string{"Expertise"})
	expertise, err := selection.AsExpertise(ctx)
	if err != nil {
		return "", err
	}
	refs := make([]*dagger.Expertise, len(expertise))
	for i := range expertise {
		refs[i] = &expertise[i]
	}
	id, err := dag.LLM().WithWorkspace(workspace).Compose(refs).ID(ctx)
	return string(id), err
}

// Attempt the effectful capture once before binding or composing tools. Capture
// is best effort: startup and reload fall back to the live workspace (with a
// warning) when git capture refuses, e.g. a dirty submodule, an oversized or
// special untracked file, or a cancelled untracked-files prompt. A recipe built
// on the live workspace is not portable, so a trace resume may not reproduce it.
func snapshotWorkspace(ctx context.Context, dag *dagger.Client) (*dagger.Workspace, error) {
	workspace := dag.CurrentWorkspace()
	id, err := workspace.Snapshot().ID(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.WarnContext(ctx, "could not snapshot workspace; continuing with the live workspace", "error", err)
		return workspace, nil
	}
	return dagger.Ref[*dagger.Workspace](dag, id), nil
}

func listAgents(ctx context.Context, dag *dagger.Client, include []string, cmd *cobra.Command) error {
	all, err := commandArtifactsWithFlags(ctx, dag, dag.CurrentWorkspace(), cmd, include, true)
	if err != nil {
		return err
	}
	return listArtifactSelection(ctx, dag, all.FilterTypes([]string{"Expertise"}), cmd)
}
