package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseUserConfig(t *testing.T) {
	t.Parallel()

	t.Run("workspace overlays with settings and envs", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseUserConfig([]byte(`
[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"

[workspaces."github.com/acme/api".env.dev.modules.aws.settings]
region = "us-west-2"

[workspaces."github.com/acme/web".modules.vercel.settings]
team = "acme-dev"
`))
		require.NoError(t, err)
		require.Len(t, cfg.Workspaces, 2)

		api := cfg.Workspaces["github.com/acme/api"]
		require.Equal(t, "alice-dev", api.Modules["aws"].Settings["profile"])
		require.Equal(t, "us-west-2", api.Env["dev"].Modules["aws"].Settings["region"])

		web := cfg.Workspaces["github.com/acme/web"]
		require.Equal(t, "acme-dev", web.Modules["vercel"].Settings["team"])
	})

	t.Run("unrelated sections are ignored", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseUserConfig([]byte(`
[llm]
default_provider = "anthropic"

[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
`))
		require.NoError(t, err)
		require.Len(t, cfg.Workspaces, 1)
	})

	t.Run("empty config", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseUserConfig(nil)
		require.NoError(t, err)
		require.Empty(t, cfg.Workspaces)
	})

	t.Run("malformed config errors", func(t *testing.T) {
		t.Parallel()
		_, err := ParseUserConfig([]byte(`[workspaces`))
		require.Error(t, err)
	})
}

func TestNormalizeGitRemote(t *testing.T) {
	t.Parallel()

	cases := []struct {
		remote string
		want   string
	}{
		// Equivalent forms of the same remote all key identically.
		{"https://github.com/acme/api", "github.com/acme/api"},
		{"https://github.com/acme/api.git", "github.com/acme/api"},
		{"http://github.com/acme/api.git", "github.com/acme/api"},
		{"git@github.com:acme/api.git", "github.com/acme/api"},
		{"git@github.com:acme/api", "github.com/acme/api"},
		{"ssh://git@github.com/acme/api.git", "github.com/acme/api"},
		{"git://github.com/acme/api.git", "github.com/acme/api"},
		{"github.com/acme/api", "github.com/acme/api"},
		{"github.com/acme/api.git", "github.com/acme/api"},
		{"GitHub.com/acme/api", "github.com/acme/api"},
		{"https://GitHub.com/acme/api", "github.com/acme/api"},

		// Other hosts, nested paths, ports.
		{"https://gitlab.example.com/team/sub/repo.git", "gitlab.example.com/team/sub/repo"},
		{"ssh://git@gitlab.example.com:2222/team/repo.git", "gitlab.example.com:2222/team/repo"},

		// Path case is preserved (only the host is case-insensitive).
		{"https://github.com/Acme/API", "github.com/Acme/API"},

		// Local filesystem remotes have no stable identity.
		{"/home/alice/src/api", ""},
		{"../api", ""},
		{"./api", ""},
		{"file:///home/alice/src/api", ""},
		{"~/src/api", ""},
		{"", ""},
	}

	for _, tc := range cases {
		t.Run(tc.remote, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, NormalizeGitRemote(tc.remote))
		})
	}
}

func TestNormalizeGitRemoteRejectsWindowsFilesystemRemotes(t *testing.T) {
	t.Parallel()

	for _, remote := range []string{
		`C:\Users\alice\api`,
		`C:/Users/alice/api`,
		`\\server\share\api`,
	} {
		t.Run(remote, func(t *testing.T) {
			t.Parallel()
			require.Empty(t, NormalizeGitRemote(remote))
		})
	}
}

func TestMatchWorkspaceOverlay(t *testing.T) {
	t.Parallel()

	cfg := &UserConfig{
		Workspaces: map[string]UserWorkspaceOverlay{
			"github.com/acme/api": {
				Modules: map[string]EnvModuleOverlay{
					"aws": {Settings: map[string]any{"profile": "alice-dev"}},
				},
			},
			// Users may key entries by any equivalent remote spelling.
			"git@github.com:acme/web.git": {
				Modules: map[string]EnvModuleOverlay{
					"vercel": {Settings: map[string]any{"team": "acme-dev"}},
				},
			},
		},
	}

	t.Run("exact key", func(t *testing.T) {
		t.Parallel()
		overlay := cfg.MatchWorkspaceOverlay("github.com/acme/api")
		require.NotNil(t, overlay)
		require.Equal(t, "alice-dev", overlay.Modules["aws"].Settings["profile"])
	})

	t.Run("equivalent remote forms match", func(t *testing.T) {
		t.Parallel()
		for _, key := range []string{
			"https://github.com/acme/api.git",
			"git@github.com:acme/api.git",
			"ssh://git@github.com/acme/api",
		} {
			overlay := cfg.MatchWorkspaceOverlay(key)
			require.NotNil(t, overlay, "key %q should match", key)
			require.Equal(t, "alice-dev", overlay.Modules["aws"].Settings["profile"])
		}
	})

	t.Run("config key in non-canonical form matches", func(t *testing.T) {
		t.Parallel()
		overlay := cfg.MatchWorkspaceOverlay("https://github.com/acme/web")
		require.NotNil(t, overlay)
		require.Equal(t, "acme-dev", overlay.Modules["vercel"].Settings["team"])
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, cfg.MatchWorkspaceOverlay("github.com/other/repo"))
	})

	t.Run("empty key never matches", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, cfg.MatchWorkspaceOverlay(""))
	})

	t.Run("nil config", func(t *testing.T) {
		t.Parallel()
		var nilCfg *UserConfig
		require.Nil(t, nilCfg.MatchWorkspaceOverlay("github.com/acme/api"))
	})
}

func TestApplyUserOverlay(t *testing.T) {
	t.Parallel()

	baseConfig := func() *Config {
		return &Config{
			Modules: map[string]ModuleEntry{
				"aws": {
					Source:   "github.com/dagger/aws",
					Settings: map[string]any{"profile": "shared", "region": "us-east-1"},
				},
			},
			Env: map[string]EnvOverlay{
				"staging": {
					Modules: map[string]EnvModuleOverlay{
						"aws": {Settings: map[string]any{"profile": "staging"}},
					},
				},
			},
		}
	}

	t.Run("user settings shadow repo settings", func(t *testing.T) {
		t.Parallel()
		applied, err := ApplyUserOverlay(baseConfig(), &UserWorkspaceOverlay{
			Modules: map[string]EnvModuleOverlay{
				"aws": {Settings: map[string]any{"profile": "alice-dev"}},
			},
		})
		require.NoError(t, err)
		require.Equal(t, "alice-dev", applied.Modules["aws"].Settings["profile"])
		// Untouched keys keep their repo values.
		require.Equal(t, "us-east-1", applied.Modules["aws"].Settings["region"])
	})

	t.Run("base config is not mutated", func(t *testing.T) {
		t.Parallel()
		base := baseConfig()
		_, err := ApplyUserOverlay(base, &UserWorkspaceOverlay{
			Modules: map[string]EnvModuleOverlay{
				"aws": {Settings: map[string]any{"profile": "alice-dev"}},
			},
			Env: map[string]EnvOverlay{
				"staging": {Modules: map[string]EnvModuleOverlay{
					"aws": {Settings: map[string]any{"profile": "mine"}},
				}},
			},
		})
		require.NoError(t, err)
		require.Equal(t, "shared", base.Modules["aws"].Settings["profile"])
		require.Equal(t, "staging", base.Env["staging"].Modules["aws"].Settings["profile"])
	})

	t.Run("user overlay adds environments", func(t *testing.T) {
		t.Parallel()
		applied, err := ApplyUserOverlay(baseConfig(), &UserWorkspaceOverlay{
			Env: map[string]EnvOverlay{
				"dev": {Modules: map[string]EnvModuleOverlay{
					"aws": {Settings: map[string]any{"profile": "alice-dev"}},
				}},
			},
		})
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"dev", "staging"}, EnvNames(applied))

		// The added env is selectable through the normal overlay path.
		envApplied, err := ApplyEnvOverlay(applied, "dev")
		require.NoError(t, err)
		require.Equal(t, "alice-dev", envApplied.Modules["aws"].Settings["profile"])
	})

	t.Run("user env merges over same-named repo env", func(t *testing.T) {
		t.Parallel()
		applied, err := ApplyUserOverlay(baseConfig(), &UserWorkspaceOverlay{
			Env: map[string]EnvOverlay{
				"staging": {Modules: map[string]EnvModuleOverlay{
					"aws": {Settings: map[string]any{"region": "eu-west-1"}},
				}},
			},
		})
		require.NoError(t, err)

		envApplied, err := ApplyEnvOverlay(applied, "staging")
		require.NoError(t, err)
		// Repo env value survives; user env value is layered on top.
		require.Equal(t, "staging", envApplied.Modules["aws"].Settings["profile"])
		require.Equal(t, "eu-west-1", envApplied.Modules["aws"].Settings["region"])
	})

	t.Run("user env values win over repo env values", func(t *testing.T) {
		t.Parallel()
		applied, err := ApplyUserOverlay(baseConfig(), &UserWorkspaceOverlay{
			Env: map[string]EnvOverlay{
				"staging": {Modules: map[string]EnvModuleOverlay{
					"aws": {Settings: map[string]any{"profile": "alice-staging"}},
				}},
			},
		})
		require.NoError(t, err)

		envApplied, err := ApplyEnvOverlay(applied, "staging")
		require.NoError(t, err)
		require.Equal(t, "alice-staging", envApplied.Modules["aws"].Settings["profile"])
	})

	t.Run("user overlay may add a module with a source", func(t *testing.T) {
		t.Parallel()
		applied, err := ApplyUserOverlay(baseConfig(), &UserWorkspaceOverlay{
			Modules: map[string]EnvModuleOverlay{
				"tailscale": {
					Source:   "github.com/acme/tailscale",
					Settings: map[string]any{"hostname": "alice"},
				},
			},
		})
		require.NoError(t, err)
		require.Equal(t, "github.com/acme/tailscale", applied.Modules["tailscale"].Source)
		require.Equal(t, "alice", applied.Modules["tailscale"].Settings["hostname"])
	})

	t.Run("unknown module without source is skipped", func(t *testing.T) {
		t.Parallel()
		// The same workspace key spans every checkout and branch of a repo, so
		// an entry for a module that does not exist here must not break the
		// workspace — it just does nothing.
		applied, err := ApplyUserOverlay(baseConfig(), &UserWorkspaceOverlay{
			Modules: map[string]EnvModuleOverlay{
				"missing": {Settings: map[string]any{"key": "value"}},
				"aws":     {Settings: map[string]any{"profile": "alice-dev"}},
			},
		})
		require.NoError(t, err)
		require.NotContains(t, applied.Modules, "missing")
		require.Equal(t, "alice-dev", applied.Modules["aws"].Settings["profile"])
	})

	t.Run("nil config passes through", func(t *testing.T) {
		t.Parallel()
		applied, err := ApplyUserOverlay(nil, &UserWorkspaceOverlay{
			Modules: map[string]EnvModuleOverlay{
				"aws": {Settings: map[string]any{"profile": "alice"}},
			},
		})
		require.NoError(t, err)
		require.Nil(t, applied)
	})

	t.Run("nil overlay passes through", func(t *testing.T) {
		t.Parallel()
		base := baseConfig()
		applied, err := ApplyUserOverlay(base, nil)
		require.NoError(t, err)
		require.Equal(t, base, applied)
	})
}

func TestWriteUserConfigValue(t *testing.T) {
	t.Parallel()

	t.Run("creates the file structure from nothing", func(t *testing.T) {
		t.Parallel()
		out, err := WriteUserConfigValue(nil, "github.com/acme/api", "modules.aws.settings.profile", "alice-dev", nil)
		require.NoError(t, err)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Equal(t, "alice-dev", cfg.Workspaces["github.com/acme/api"].Modules["aws"].Settings["profile"])
	})

	t.Run("preserves unrelated sections and workspaces", func(t *testing.T) {
		t.Parallel()
		existing := []byte(`[llm]
default_provider = "anthropic"

[workspaces."github.com/acme/web".modules.vercel.settings]
team = "acme-dev"
`)
		out, err := WriteUserConfigValue(existing, "github.com/acme/api", "modules.aws.settings.profile", "alice-dev", nil)
		require.NoError(t, err)
		require.Contains(t, string(out), `default_provider = "anthropic"`)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Equal(t, "acme-dev", cfg.Workspaces["github.com/acme/web"].Modules["vercel"].Settings["team"])
		require.Equal(t, "alice-dev", cfg.Workspaces["github.com/acme/api"].Modules["aws"].Settings["profile"])
	})

	t.Run("workspace key is canonicalized", func(t *testing.T) {
		t.Parallel()
		out, err := WriteUserConfigValue(nil, "git@github.com:acme/api.git", "modules.aws.settings.profile", "alice-dev", nil)
		require.NoError(t, err)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Contains(t, cfg.Workspaces, "github.com/acme/api")
	})

	t.Run("existing equivalent entry is updated in place", func(t *testing.T) {
		t.Parallel()
		existing := []byte(`[workspaces."https://github.com/acme/api.git".modules.aws.settings]
profile = "old"
`)
		out, err := WriteUserConfigValue(existing, "github.com/acme/api", "modules.aws.settings.profile", "alice-dev", nil)
		require.NoError(t, err)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Len(t, cfg.Workspaces, 1)
		require.Equal(t, "alice-dev", cfg.Workspaces["https://github.com/acme/api.git"].Modules["aws"].Settings["profile"])
	})

	t.Run("env-scoped keys", func(t *testing.T) {
		t.Parallel()
		out, err := WriteUserConfigValue(nil, "github.com/acme/api", "env.dev.modules.aws.settings.region", "us-west-2", nil)
		require.NoError(t, err)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Equal(t, "us-west-2", cfg.Workspaces["github.com/acme/api"].Env["dev"].Modules["aws"].Settings["region"])
	})

	t.Run("values are typed like repository config writes", func(t *testing.T) {
		t.Parallel()
		out, err := WriteUserConfigValue(nil, "github.com/acme/api", "modules.aws.settings.retries", "3", nil)
		require.NoError(t, err)
		require.Contains(t, string(out), "retries = 3")

		out, err = WriteUserConfigValue(nil, "github.com/acme/api", "modules.aws.settings.tags", "a, b", nil)
		require.NoError(t, err)
		require.Contains(t, string(out), `tags = ["a", "b"]`)
	})

	t.Run("explicit list values round-trip verbatim", func(t *testing.T) {
		t.Parallel()
		out, err := WriteUserConfigValue(nil, "github.com/acme/api", "modules.aws.settings.tags", "", []string{"a,b", "c"})
		require.NoError(t, err)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Equal(t, []any{"a,b", "c"}, cfg.Workspaces["github.com/acme/api"].Modules["aws"].Settings["tags"])
	})

	t.Run("only module settings keys are allowed", func(t *testing.T) {
		t.Parallel()
		for _, key := range []string{
			"modules.aws.source",
			"modules.aws.entrypoint",
			"ignore",
			"defaults_from_dotenv",
			"env.dev.modules.aws.source",
		} {
			_, err := WriteUserConfigValue(nil, "github.com/acme/api", key, "x", nil)
			require.Error(t, err, "key %q should be rejected", key)
		}
	})

	t.Run("unusable workspace key errors", func(t *testing.T) {
		t.Parallel()
		_, err := WriteUserConfigValue(nil, "/home/alice/src/api", "modules.aws.settings.profile", "x", nil)
		require.ErrorContains(t, err, "not a usable git remote")
	})
}

func TestDeleteUserConfigValue(t *testing.T) {
	t.Parallel()

	t.Run("removes the key and prunes empty tables", func(t *testing.T) {
		t.Parallel()
		existing := []byte(`[llm]
default_provider = "anthropic"

[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
`)
		out, err := DeleteUserConfigValue(existing, "github.com/acme/api", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Contains(t, string(out), `default_provider = "anthropic"`)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Empty(t, cfg.Workspaces)
	})

	t.Run("keeps sibling values and workspaces", func(t *testing.T) {
		t.Parallel()
		existing := []byte(`[workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
region = "us-west-2"

[workspaces."github.com/acme/web".modules.vercel.settings]
team = "acme-dev"
`)
		out, err := DeleteUserConfigValue(existing, "github.com/acme/api", "modules.aws.settings.profile")
		require.NoError(t, err)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Equal(t, "us-west-2", cfg.Workspaces["github.com/acme/api"].Modules["aws"].Settings["region"])
		require.Equal(t, "acme-dev", cfg.Workspaces["github.com/acme/web"].Modules["vercel"].Settings["team"])
	})

	t.Run("matches equivalent workspace key spellings", func(t *testing.T) {
		t.Parallel()
		existing := []byte(`[workspaces."git@github.com:acme/api.git".modules.aws.settings]
profile = "alice-dev"
`)
		out, err := DeleteUserConfigValue(existing, "github.com/acme/api", "modules.aws.settings.profile")
		require.NoError(t, err)

		cfg, err := ParseUserConfig(out)
		require.NoError(t, err)
		require.Empty(t, cfg.Workspaces)
	})

	t.Run("missing key errors", func(t *testing.T) {
		t.Parallel()
		_, err := DeleteUserConfigValue(nil, "github.com/acme/api", "modules.aws.settings.profile")
		require.ErrorContains(t, err, "is not set in user-level config")
	})
}

func TestGitRemoteURL(t *testing.T) {
	t.Parallel()

	t.Run("origin url", func(t *testing.T) {
		t.Parallel()
		url, ok := GitRemoteURL([]byte(`
[core]
	repositoryformatversion = 0
[remote "origin"]
	url = git@github.com:acme/api.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`), "origin")
		require.True(t, ok)
		require.Equal(t, "git@github.com:acme/api.git", url)
	})

	t.Run("multiple remotes selects the named one", func(t *testing.T) {
		t.Parallel()
		config := []byte(`
[remote "upstream"]
	url = https://github.com/acme/api.git
[remote "origin"]
	url = git@github.com:alice/api.git
`)
		url, ok := GitRemoteURL(config, "origin")
		require.True(t, ok)
		require.Equal(t, "git@github.com:alice/api.git", url)

		url, ok = GitRemoteURL(config, "upstream")
		require.True(t, ok)
		require.Equal(t, "https://github.com/acme/api.git", url)
	})

	t.Run("no remote", func(t *testing.T) {
		t.Parallel()
		_, ok := GitRemoteURL([]byte("[core]\n\tbare = false\n"), "origin")
		require.False(t, ok)
	})

	t.Run("comments and blank lines are skipped", func(t *testing.T) {
		t.Parallel()
		url, ok := GitRemoteURL([]byte(`
; comment
[remote "origin"]
	# comment
	url = https://github.com/acme/api.git
`), "origin")
		require.True(t, ok)
		require.Equal(t, "https://github.com/acme/api.git", url)
	})
}

func TestParseGitDirFile(t *testing.T) {
	t.Parallel()

	t.Run("worktree gitdir", func(t *testing.T) {
		t.Parallel()
		dir, ok := ParseGitDirFile([]byte("gitdir: /home/alice/src/api/.git/worktrees/feature\n"))
		require.True(t, ok)
		require.Equal(t, "/home/alice/src/api/.git/worktrees/feature", dir)
	})

	t.Run("relative gitdir", func(t *testing.T) {
		t.Parallel()
		dir, ok := ParseGitDirFile([]byte("gitdir: ../.git/modules/sub"))
		require.True(t, ok)
		require.Equal(t, "../.git/modules/sub", dir)
	})

	t.Run("not a gitdir file", func(t *testing.T) {
		t.Parallel()
		_, ok := ParseGitDirFile([]byte("ref: refs/heads/main"))
		require.False(t, ok)
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		_, ok := ParseGitDirFile(nil)
		require.False(t, ok)
	})
}
