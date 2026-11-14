package schema

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestWorkspaceCommitOptions(t *testing.T) {
	args := workspaceWithCommitArgs{Message: "commit", Date: "2026-09-05T12:00:00Z", AuthorName: dagql.Opt(dagql.NewString("Explicit Author")), AuthorEmail: dagql.Opt(dagql.NewString("explicit@example.com"))}
	opts, err := args.opts()
	require.NoError(t, err)
	require.Equal(t, "Explicit Author", opts.AuthorName)
	require.Equal(t, "explicit@example.com", opts.AuthorEmail)
	args.AuthorName = dagql.Optional[dagql.String]{}
	args.AuthorEmail = dagql.Optional[dagql.String]{}
	fallback, err := args.opts()
	require.NoError(t, err)
	require.Equal(t, "Dagger", fallback.AuthorName)
	require.Equal(t, "dagger@localhost", fallback.AuthorEmail)
	for _, date := range []string{"", "now", "2026-09-05"} {
		args.Date = date
		_, err := args.opts()
		require.ErrorContains(t, err, "RFC3339")
	}
	args.Date = "2026-09-05T12:00:00Z"
	for _, message := range []string{"", " \n", "subject\x00body"} {
		args.Message = message
		_, err := args.opts()
		require.ErrorContains(t, err, "message must be nonempty and contain no NUL")
	}
}

func TestWorkspaceGitAuthorValidation(t *testing.T) {
	for _, invalid := range []string{"name\ntrailer", "name\rtrailer", "name\x00trailer", "Name <email>"} {
		require.Error(t, validateWorkspaceGitAuthor(invalid, "valid@example.com"))
		require.Error(t, validateWorkspaceGitAuthor("Valid", invalid))
	}
	require.NoError(t, validateWorkspaceGitAuthor("Zoë", "zoe@example.com"))
	require.NoError(t, validateWorkspaceGitAuthor("", ""))
}

type gitIdentityServerBase interface{ core.Server }

type gitIdentityServer struct {
	gitIdentityServerBase // Anything but AuthorizeGitIdentity would panic.
	identity              core.GitIdentity
	err                   error
}

func (s *gitIdentityServer) AuthorizeGitIdentity(context.Context) (core.GitIdentity, error) {
	return s.identity, s.err
}

func TestWorkspaceGitAuthor(t *testing.T) {
	traceID := trace.TraceID{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}
	notShared := func(agent, module string) error {
		return &core.GitIdentityNotSharedError{Agent: agent, Module: module, Reason: errors.New("denied")}
	}
	for _, tc := range []struct {
		name              string
		srv               *gitIdentityServer
		traced            bool
		wantName, wantErr string
		wantEmail         string
	}{
		{name: "shared", srv: &gitIdentityServer{identity: core.GitIdentity{Name: "Personal", Email: "personal@example.com"}}, wantName: "Personal", wantEmail: "personal@example.com"},
		{name: "partial", srv: &gitIdentityServer{identity: core.GitIdentity{Email: "personal@example.com"}}, wantName: "Dagger", wantEmail: "personal@example.com"},
		{name: "unconfigured", srv: &gitIdentityServer{}, traced: true, wantName: "Dagger", wantEmail: "dagger@localhost"},
		// Not sharing means "don't attribute to me", not "don't commit": the
		// commit is attributed to whoever asked, in the trace that shows what
		// they did.
		{name: "not shared with an agent", traced: true,
			srv:      &gitIdentityServer{identity: core.GitIdentity{Name: "Leaked"}, err: notShared("worker", "staff")},
			wantName: "worker", wantEmail: "worker@0123456789abcdef0123456789abcdef"},
		{name: "not shared with a module", traced: true,
			srv:      &gitIdentityServer{err: notShared("", "committer")},
			wantName: "committer", wantEmail: "committer@0123456789abcdef0123456789abcdef"},
		{name: "not shared with nobody named", traced: true,
			srv:      &gitIdentityServer{err: fmt.Errorf("%w: cannot prompt", core.ErrGitIdentityNotShared)},
			wantName: "Dagger", wantEmail: "dagger@0123456789abcdef0123456789abcdef"},
		{name: "not shared with an odd name", traced: true,
			srv:      &gitIdentityServer{err: notShared("Zoë <the> bot\n", "")},
			wantName: "Zoë the bot", wantEmail: "Zo-the-bot@0123456789abcdef0123456789abcdef"},
		{name: "not shared with a name with no address", traced: true,
			srv:      &gitIdentityServer{err: notShared("✨", "committer")},
			wantName: "✨", wantEmail: "committer@0123456789abcdef0123456789abcdef"},
		{name: "not shared outside a trace",
			srv:      &gitIdentityServer{identity: core.GitIdentity{Name: "Leaked"}, err: notShared("worker", "staff")},
			wantName: "Dagger", wantEmail: "dagger@localhost"},
		{name: "failed", srv: &gitIdentityServer{err: errors.New("prompt failed")}, wantErr: "prompt failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := core.ContextWithQuery(t.Context(), &core.Query{Server: tc.srv})
			if tc.traced {
				ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: trace.SpanID{1}}))
			}
			name, email, err := workspaceGitAuthor(ctx)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantName, name)
			require.Equal(t, tc.wantEmail, email)
			require.NoError(t, validateWorkspaceGitAuthor(name, email))
		})
	}
}

func TestGitEmailLocalPart(t *testing.T) {
	for in, want := range map[string]string{
		"worker":               "worker",
		"keeper-worker":        "keeper-worker",
		"Staff worker #1":      "Staff-worker-1",
		"..a..b--c.-d..":       "a.b-c.d",
		"bot+ci_1":             "bot+ci_1",
		"<évil>@host":          "vil-host",
		"":                     "",
		"✨":                    "",
		strings.Repeat("a", 70): strings.Repeat("a", 64),
	} {
		require.Equal(t, want, gitEmailLocalPart(in), "%q", in)
	}
}
