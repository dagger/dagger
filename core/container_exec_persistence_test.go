package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
)

func TestContainerExecPersistsInputMetadata(t *testing.T) {
	for name, input := range map[string]*engineutil.ExecutionMetadata{
		"nil": nil,
		"supplied": {
			Internal:           true,
			HostAliases:        map[string][]string{"service": {"input-alias"}},
			ExtraSearchDomains: []string{"input-domain"},
			SecretEnvNames:     []string{"INPUT_SECRET"},
			RedirectStdoutPath: "/input-output",
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := newPersistedFamiliesTestEnv(t, "exec-inputs")
			ctx, cache, srv := env.open(t)
			platform := Platform{OS: "linux", Architecture: "amd64"}
			parent := env.attach(t, ctx, cache, srv, "exec-input-parent", NewContainer(platform)).(dagql.ObjectResult[*Container])
			inputJSON, err := json.Marshal(input)
			require.NoError(t, err)
			opts := ContainerExecOpts{Args: []string{"echo", "$VALUE"}, Expand: true, RedirectStdout: "/run-output"}
			child := NewContainer(platform)
			require.NoError(t, child.WithExec(ctx, parent, opts, input, dagql.ObjectResult[*Module]{}, nil))
			childRes := env.attach(t, ctx, cache, srv, "withExec", child)
			childID, err := cache.PersistedResultID(childRes)
			require.NoError(t, err)
			frame, err := childRes.ResultCall()
			require.NoError(t, err)
			recipe := child.Lazy.(*ContainerExecLazy)

			// Use the actual runtime metadata preparation. No command is run.
			runtimeContainer := NewContainer(platform)
			runtimeContainer.Services = ServiceBindings{{Hostname: "service", Aliases: AliasSet{"run-alias"}}}
			runtimeContainer.Secrets = []ContainerSecret{{EnvName: "RUN_SECRET"}}
			runtimeContainer.SystemEnvNames = []string{"RUN_ENV"}
			recipe.State.ExecMD, err = runtimeContainer.execMeta(ctx, opts, recipe.State.ExecMD, recipe.State.ModuleContext)
			require.NoError(t, err)
			require.Contains(t, recipe.State.ExecMD.HostAliases["service"], "run-alias")
			require.Equal(t, "/run-output", recipe.State.ExecMD.RedirectStdoutPath)

			enc := dagql.NewPersistEncodeContext(cache, childID, frame)
			encoded, err := child.EncodePersistedObject(ctx, enc)
			require.NoError(t, err)
			var payload persistedContainerPayload
			require.NoError(t, json.Unmarshal(encoded.JSON, &payload))
			var saved persistedContainerExecLazy
			require.NoError(t, json.Unmarshal(payload.LazyJSON, &saved))
			savedJSON, err := json.Marshal(saved.ExecMD)
			require.NoError(t, err)
			require.JSONEq(t, string(inputJSON), string(savedJSON))
			require.Equal(t, opts, saved.Opts)
			parentID, err := cache.PersistedResultID(parent)
			require.NoError(t, err)
			require.Equal(t, parentID, saved.ParentResultID)

			value, err := (&Container{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, childID, frame), encoded.JSON)
			require.NoError(t, err)
			restored := value.(*Container)
			restoredRecipe := restored.Lazy.(*ContainerExecLazy)
			restoredJSON, err := json.Marshal(restoredRecipe.State.ExecMD)
			require.NoError(t, err)
			require.JSONEq(t, string(inputJSON), string(restoredJSON))

			// A run after decode must not modify the next saved input either.
			restoredRecipe.State.ExecMD, err = runtimeContainer.execMeta(ctx, opts, restoredRecipe.State.ExecMD, restoredRecipe.State.ModuleContext)
			require.NoError(t, err)
			reencoded, err := restored.EncodePersistedObject(ctx, enc)
			require.NoError(t, err)
			require.JSONEq(t, string(encoded.JSON), string(reencoded.JSON))
		})
	}
}
