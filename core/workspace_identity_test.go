package core

import (
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	workspacepkg "github.com/dagger/dagger/core/workspace"
)

// TestWorkspaceContentIdentity checks that a workspace holding its own content
// gets an identity from everything a caller can observe through it: equal
// workspaces share one, and a change to any observable field gives another.
func TestWorkspaceContentIdentity(t *testing.T) {
	t.Parallel()
	f := newWorkspaceRelationFixture(t)

	identity := func(ws *Workspace) digest.Digest {
		t.Helper()
		dgst, ok, err := ws.ContentIdentity(f.ctx)
		require.NoError(t, err)
		require.True(t, ok, "a workspace holding its own content must have an identity")
		require.NotEmpty(t, dgst)
		return dgst
	}

	base := func() *Workspace {
		ws := gitRefWorkspace(f.gitRef("https://example.com/repo.git", "main", shaA))
		ws.Cwd = "."
		ws.ConfigFile = "dagger.toml"
		ws.LockFile = "dagger.lock"
		return ws
	}

	t.Run("equal workspaces share one identity", func(t *testing.T) {
		require.Equal(t, identity(base()), identity(base()))
	})

	t.Run("the client route is not part of it", func(t *testing.T) {
		a, b := base(), base()
		a.ClientID = "client-a"
		b.ClientID = "client-b"
		require.Equal(t, identity(a), identity(b))
	})

	changes := map[string]func(*Workspace) *Workspace{
		"commit": func(*Workspace) *Workspace {
			ws := base()
			ws.SetSource(NewWorkspaceSourceGitRef(f.gitRef("https://example.com/repo.git", "main", shaB)))
			return ws
		},
		"cwd": func(ws *Workspace) *Workspace {
			ws.Cwd = "sub"
			return ws
		},
		"config file": func(ws *Workspace) *Workspace {
			ws.ConfigFile = "other/dagger.toml"
			return ws
		},
		"lock file": func(ws *Workspace) *Workspace {
			ws.LockFile = "other/dagger.lock"
			return ws
		},
		"address": func(ws *Workspace) *Workspace {
			ws.Address = "git-ref://elsewhere"
			return ws
		},
		"overlay": withOverlay,
		"mounts": func(ws *Workspace) *Workspace {
			ws.mounts = f.directory("mounted")
			ws.mountPoints = []string{"vendor"}
			return ws
		},
		"rootfs": func(ws *Workspace) *Workspace {
			ws.SetRootfs(f.directory("other-root"))
			return ws
		},
		"selected env": func(ws *Workspace) *Workspace {
			ws.SetSelectedEnv("ci")
			return ws
		},
		"module clients": func(ws *Workspace) *Workspace {
			ws.SetModuleClients(map[string][]string{".dagger/modules/a": {".dagger/modules/b"}})
			return ws
		},
		"client modules": func(ws *Workspace) *Workspace {
			ws.SetClientModules([]WorkspaceClientModule{{Name: "extra", Digest: digest.FromString("extra")}})
			return ws
		},
		"a client module's main flag": func(ws *Workspace) *Workspace {
			ws.SetClientModules([]WorkspaceClientModule{{Name: "extra", Digest: digest.FromString("extra"), Main: true}})
			return ws
		},
		"user config key": func(ws *Workspace) *Workspace {
			ws.SetUserConfigKey("github.com/acme/repo")
			return ws
		},
		"user config overlay": func(ws *Workspace) *Workspace {
			ws.SetUserConfigOverlay(&workspacepkg.UserWorkspaceOverlay{
				Modules: map[string]workspacepkg.EnvModuleOverlay{"greeter": {Settings: map[string]any{"greeting": "hola"}}},
			})
			return ws
		},
		"compat workspace": func(ws *Workspace) *Workspace {
			ws.SetCompatWorkspace(&workspacepkg.CompatWorkspace{})
			return ws
		},
	}
	for name, change := range changes {
		t.Run(name+" gives another identity", func(t *testing.T) {
			require.NotEqual(t, identity(base()), identity(change(base())))
		})
	}

	t.Run("a directory-backed workspace has one", func(t *testing.T) {
		root := f.directory("root")
		require.Equal(t, identity(directoryWorkspace(root, "dir://a")), identity(directoryWorkspace(root, "dir://a")))
		require.NotEqual(t, identity(directoryWorkspace(root, "dir://a")), identity(directoryWorkspace(f.directory("other"), "dir://a")))
	})

	t.Run("host-backed and rootless workspaces have none", func(t *testing.T) {
		for name, ws := range map[string]*Workspace{
			"host checkout":            localWorkspace("/host/ws", "client", ""),
			"host checkout with edits": withOverlay(localWorkspace("/host/ws", "client", "")),
			"rootless": func() *Workspace {
				ws := &Workspace{Address: "file:///host/ws", ClientID: "client"}
				ws.SetHostPath("/host/ws")
				ws.SetSource(NewWorkspaceSourceRootlessLocal("/host/ws"))
				return ws
			}(),
		} {
			_, ok, err := ws.ContentIdentity(f.ctx)
			require.NoError(t, err, name)
			require.False(t, ok, "%s must keep its per-call key", name)
		}
	})
}

// TestWorkspaceContentIdentityCoversEveryField fails when a field is added to
// Workspace without deciding whether ContentIdentity covers it: a field it
// misses would let two different workspaces share cache hits.
func TestWorkspaceContentIdentityCoversEveryField(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{
		"source": true, "rootfs": true, "mounts": true, "mountPoints": true,
		"compatWorkspace": true, "userConfigKey": true, "userConfigOverlay": true,
		"selectedEnv": true, "moduleClients": true, "clientModules": true,
		"Address": true, "Cwd": true, "ConfigFile": true, "LockFile": true,
	}
	// The client route says where host operations go, not what the
	// workspace holds.
	excluded := map[string]bool{"ClientID": true, "hostPath": true}

	typ := reflect.TypeOf(Workspace{})
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		require.True(t, covered[name] || excluded[name],
			"Workspace.%s is neither covered by ContentIdentity nor excluded from it", name)
	}
	require.Equal(t, len(covered)+len(excluded), typ.NumField())
}
