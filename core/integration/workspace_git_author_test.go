package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func (WorkspaceSuite) TestWorkspaceWithCommitGitConfigIdentity(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c).
		WithEnvVariable("GIT_CONFIG_NOSYSTEM", "1").
		WithEnvVariable("GIT_CONFIG_GLOBAL", "/tmp/author.gitconfig")
	repoURL, err := base.WithExec([]string{"git", "remote", "get-url", "origin"}).Stdout(ctx)
	require.NoError(t, err)
	repoURL = strings.TrimSpace(repoURL)
	for _, tc := range []struct {
		name, global, localName, localEmail, wantName, wantEmail string
		authorArgs                                               string
		value, outside                                           bool
	}{
		{name: "checkout inherits global", global: "[user]\nname = Personal\nemail = personal@example.com\n", wantName: "Personal", wantEmail: "personal@example.com"},
		{name: "checkout overrides global", global: "[user]\nname = Personal\nemail = personal@example.com\n", localName: "Work", localEmail: "work@example.com", wantName: "Work", wantEmail: "work@example.com"},
		{name: "checkout overrides email only", global: "[user]\nname = Personal\nemail = personal@example.com\n", localEmail: "work@example.com", wantName: "Personal", wantEmail: "work@example.com"},
		{name: "value uses caller checkout overrides", value: true, global: "[user]\nname = Personal\nemail = personal@example.com\n", localName: "Work", localEmail: "work@example.com", wantName: "Work", wantEmail: "work@example.com"},
		{name: "value outside checkout uses global", value: true, outside: true, global: "[user]\nname = Personal\nemail = personal@example.com\n", localName: "Work", localEmail: "work@example.com", wantName: "Personal", wantEmail: "personal@example.com"},
		{name: "same value different client config", value: true, global: "[user]\nname = Another\nemail = another@example.com\n", wantName: "Another", wantEmail: "another@example.com"},
		{name: "value without config", value: true, wantName: "Dagger", wantEmail: "dagger@localhost"},
		{name: "value with partial config", value: true, global: "[user]\nemail = personal@example.com\n", wantName: "Dagger", wantEmail: "personal@example.com"},
		{name: "checkout without config", wantName: "Dagger", wantEmail: "dagger@localhost"},
		{name: "explicit name", value: true, global: "[user]\nname = Personal\nemail = personal@example.com\n", authorArgs: `, authorName: "Explicit"`, wantName: "Explicit", wantEmail: "personal@example.com"},
		{name: "explicit email", value: true, global: "[user]\nname = Personal\nemail = personal@example.com\n", authorArgs: `, authorEmail: "explicit@example.com"`, wantName: "Personal", wantEmail: "explicit@example.com"},
		{name: "explicit identity", value: true, authorArgs: `, authorName: "Explicit", authorEmail: "explicit@example.com"`, wantName: "Explicit", wantEmail: "explicit@example.com"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			configured := base.WithNewFile("/tmp/author.gitconfig", tc.global)
			if tc.localName != "" {
				configured = configured.WithExec([]string{"git", "config", "user.name", tc.localName})
			}
			if tc.localEmail != "" {
				configured = configured.WithExec([]string{"git", "config", "user.email", tc.localEmail})
			}
			if tc.outside {
				configured = configured.WithWorkdir("/tmp")
			}
			selection := `currentWorkspace { %s }`
			resultPath := "currentWorkspace"
			if tc.value {
				selection = fmt.Sprintf(`git(url: %q) { head { asWorkspace { %%s } } }`, repoURL)
				resultPath = "git.head.asWorkspace"
			}
			query := "{" + fmt.Sprintf(selection, `withNewFile(path: "authored.txt", contents: "authored") {
				withCommit(message: "authored", date: "2026-09-05T12:00:00Z" __AUTHOR_ARGS__) {
					git { head { targetCommit { authorName authorEmail committerName committerEmail } } }
				}
			}`) + "}"
			query = strings.ReplaceAll(query, "__AUTHOR_ARGS__", tc.authorArgs)
			out, err := configured.With(daggerQuery(query)).Stdout(ctx)
			require.NoError(t, err)
			commit := gjson.Get(out, resultPath+".withNewFile.withCommit.git.head.targetCommit")
			require.True(t, commit.Exists(), out)
			require.Equal(t, tc.wantName, commit.Get("authorName").String())
			require.Equal(t, tc.wantEmail, commit.Get("authorEmail").String())
			require.Equal(t, tc.wantName, commit.Get("committerName").String())
			require.Equal(t, tc.wantEmail, commit.Get("committerEmail").String())
		})
	}
}

func (WorkspaceSuite) TestWorkspaceWithCommitResolvedIdentityReplay(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c).
		WithEnvVariable("GIT_CONFIG_NOSYSTEM", "1").
		WithEnvVariable("GIT_CONFIG_GLOBAL", "/tmp/author.gitconfig")
	repoURL, err := base.WithExec([]string{"git", "remote", "get-url", "origin"}).Stdout(ctx)
	require.NoError(t, err)
	repoURL = strings.TrimSpace(repoURL)
	for _, tc := range []struct{ name, config, wantName, wantEmail string }{
		{"configured", "[user]\nname = Personal\nemail = personal@example.com\n", "Personal", "personal@example.com"},
		{"unconfigured", "", "Dagger", "dagger@localhost"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			original := base.WithNewFile("/tmp/author.gitconfig", tc.config)
			recipe, err := original.With(daggerShell(fmt.Sprintf(`llm | with-workspace --workspace $(git --url %s | head | as-workspace | with-new-file authored.txt authored | with-commit --message authored --date 2026-09-05T12:00:00Z) | portable-id`, repoURL))).Stdout(ctx)
			require.NoError(t, err)
			recipe = strings.TrimSpace(recipe)
			// The original client is gone. Replaying the commit retains its
			// resolved identity, including explicit fallbacks for missing config.
			restored := dagger.Ref[*dagger.LLM](c, dagger.ID(recipe)).Workspace()
			name, err := restored.Git().Head().TargetCommit().AuthorName(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.wantName, name)
			email, err := restored.Git().Head().TargetCommit().AuthorEmail(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.wantEmail, email)
			parents, err := restored.Git().Head().TargetCommit().ParentShas(ctx)
			require.NoError(t, err)
			require.Len(t, parents, 1)
			// A new commit samples the restoring client, rather than inheriting
			// the identity of the earlier commit or checkpoint.
			query := fmt.Sprintf(`{ node(id: %q) { ... on LLM { workspace {
				withReset(commit: %q) { withCommit(message: "amended", date: "2026-09-05T12:00:00Z") {
					git { head { targetCommit { authorName authorEmail committerName committerEmail } } }
				} }
			} } } }`, recipe, parents[0])
			out, err := base.WithNewFile("/tmp/author.gitconfig", "[user]\nname = Restorer\nemail = restorer@example.com\n").
				With(daggerQuery(query)).Stdout(ctx)
			require.NoError(t, err)
			commit := gjson.Get(out, "node.workspace.withReset.withCommit.git.head.targetCommit")
			require.True(t, commit.Exists(), out)
			require.Equal(t, "Restorer", commit.Get("authorName").String())
			require.Equal(t, "restorer@example.com", commit.Get("authorEmail").String())
			require.Equal(t, "Restorer", commit.Get("committerName").String())
			require.Equal(t, "restorer@example.com", commit.Get("committerEmail").String())
		})
	}
}
