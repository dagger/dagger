package llbtodagger

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/dockerfile2llb"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerui"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func findRootContainerCall(id *call.ID) *call.ID {
	for cur := id; cur != nil; cur = cur.Receiver() {
		if cur.Field() == "container" && cur.Receiver() == nil {
			return cur
		}
	}
	return nil
}

func containerPlatformArg(t *testing.T, id *call.ID) (string, bool) {
	t.Helper()
	ctrCall := findRootContainerCall(id)
	require.NotNil(t, ctrCall, "expected root container call in recipe %s", id.Display())
	arg := ctrCall.Arg("platform")
	if arg == nil {
		return "", false
	}
	lit, ok := arg.Value().(*call.LiteralString)
	require.True(t, ok, "expected string literal for platform arg, got %T", arg.Value())
	return lit.Value(), true
}

func TestDefinitionToIDFromScratchPreservesTargetPlatform(t *testing.T) {
	ctx := context.Background()
	buildPlatform := ocispecs.Platform{OS: "linux", Architecture: "arm64"}
	targetPlatform := ocispecs.Platform{OS: "linux", Architecture: "amd64"}

	convertDockerfile := func(t *testing.T, dockerfile string, target *ocispecs.Platform) *call.ID {
		t.Helper()
		mainCtx := DockerfileMainContextSentinelState()
		st, img, _, _, err := dockerfile2llb.Dockerfile2LLB(ctx, []byte(dockerfile), dockerfile2llb.ConvertOpt{
			Config: dockerui.Config{
				BuildPlatforms: []ocispecs.Platform{buildPlatform},
			},
			MainContext:    &mainCtx,
			TargetPlatform: target,
		})
		require.NoError(t, err)

		marshalPlatform := buildPlatform
		if target != nil {
			marshalPlatform = *target
		}
		def, err := st.Marshal(ctx, llb.Platform(marshalPlatform))
		require.NoError(t, err)

		mainDirID := appendCall(call.New(), directoryType(), "directory")
		id, err := DefinitionToIDWithOptions(def.ToPB(), img, DefinitionToIDOptions{
			MainContextDirectoryID: mainDirID,
		})
		require.NoError(t, err)
		return id
	}

	t.Run("empty FROM scratch stage", func(t *testing.T) {
		id := convertDockerfile(t, "FROM scratch\nLABEL org.example.test=1\n", &targetPlatform)
		got, ok := containerPlatformArg(t, id)
		require.True(t, ok)
		require.Equal(t, "linux/amd64", got)
	})

	t.Run("FROM scratch with COPY from context", func(t *testing.T) {
		id := convertDockerfile(t, "FROM scratch\nCOPY hello /hello\n", &targetPlatform)
		got, ok := containerPlatformArg(t, id)
		require.True(t, ok)
		require.Equal(t, "linux/amd64", got)
	})

	t.Run("multi-stage build with FROM --platform=$BUILDPLATFORM and final FROM scratch", func(t *testing.T) {
		dockerfile := `FROM --platform=$BUILDPLATFORM scratch AS build
COPY src /out
FROM scratch
COPY --from=build /out /out
ENTRYPOINT ["/out"]
`
		id := convertDockerfile(t, dockerfile, &targetPlatform)
		got, ok := containerPlatformArg(t, id)
		require.True(t, ok)
		require.Equal(t, "linux/amd64", got)
	})

	t.Run("platform variant preserved", func(t *testing.T) {
		armV7 := ocispecs.Platform{OS: "linux", Architecture: "arm", Variant: "v7"}
		id := convertDockerfile(t, "FROM scratch\nCOPY hello /hello\n", &armV7)
		got, ok := containerPlatformArg(t, id)
		require.True(t, ok)
		require.Equal(t, "linux/arm/v7", got)
	})

	t.Run("nil image keeps default platform", func(t *testing.T) {
		id, err := DefinitionToID(nil, nil)
		require.NoError(t, err)
		_, ok := containerPlatformArg(t, id)
		require.False(t, ok)
	})
}
