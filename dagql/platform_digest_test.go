package dagql

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
)

// platformTestBuild names where a recipe takes a platform: a container's
// platform argument, and a command, Container.withExec.
func platformTestBuild() BuildPlatform {
	return BuildPlatform{
		ImplicitInput: "engineDefaultPlatform",
		IsPlatformArg: func(receiverType, field string, _ call.View, arg string) bool {
			return receiverType == "Query" && field == "container" && arg == "platform"
		},
		IsCommand: func(receiverType, field string) bool {
			return receiverType == "Container" && field == "withExec"
		},
	}
}

func platformTestString(name, value string) *ResultCallArg {
	return &ResultCallArg{Name: name, Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: value}}
}

func platformTestCall(typ, field string, receiver *ResultCall, args ...*ResultCallArg) *ResultCall {
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  &ResultCallType{NamedType: typ, NonNull: true},
		Field: field,
		Args:  args,
	}
	if receiver != nil {
		frame.Receiver = &ResultCallRef{Call: receiver}
	}
	return frame
}

// platformTestBuildOutput is a build's output on an engine of platform: a
// container of the engine's platform from image, with GOARCH, that runs
// script, mounting lib, and its output directory.
func platformTestBuildOutput(platform, goarch, script string, lib *ResultCall) *ResultCall {
	return platformTestBuildOutputFrom(platform, goarch, script, lib, false)
}

func platformTestBuildOutputFrom(platform, goarch, script string, lib *ResultCall, libDir bool) *ResultCall {
	ctr := platformTestCall("Container", "container", nil, platformTestString("platform", platform))
	ctr = platformTestCall("Container", "from", ctr, platformTestString("address", "golang@sha256:index"))
	ctr = platformTestCall("Container", "withEnvVariable", ctr, platformTestString("name", "GOARCH"), platformTestString("value", goarch))
	if lib != nil {
		field := "withMountedFile"
		if libDir {
			field = "withMountedDirectory"
		}
		ctr = platformTestCall("Container", field, ctr, platformTestString("path", "/lib"),
			&ResultCallArg{Name: "source", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{Call: lib}}})
	}
	ctr = platformTestCall("Container", "withExec", ctr, platformTestString("args", script))
	return platformTestCall("Directory", "directory", ctr, platformTestString("path", "/out"))
}

// platformTestDigest is frame's identity declared for platform.
func platformTestDigest(t *testing.T, frame *ResultCall, platform string) digest.Digest {
	t.Helper()
	dig, err := (*Cache)(nil).PlatformDigest(frame, platform, platformTestBuild())
	require.NoError(t, err)
	return dig
}

func TestPlatformDigest(t *testing.T) {
	const amd64, arm64 = "linux/amd64", "linux/arm64"
	native := platformTestDigest(t, platformTestBuildOutput(arm64, "arm64", "compile", nil), arm64)

	t.Run("an engine of another platform making the same result agrees", func(t *testing.T) {
		require.Equal(t, native, platformTestDigest(t, platformTestBuildOutput(amd64, "arm64", "compile", nil), arm64))
	})
	t.Run("the recipe digests differ", func(t *testing.T) {
		require.NotEqual(t,
			cacheTestCallDigest(platformTestBuildOutput(arm64, "arm64", "compile", nil)),
			cacheTestCallDigest(platformTestBuildOutput(amd64, "arm64", "compile", nil)))
	})
	t.Run("the declared platform counts", func(t *testing.T) {
		require.NotEqual(t, native, platformTestDigest(t, platformTestBuildOutput(arm64, "arm64", "compile", nil), amd64))
	})
	t.Run("what the work does counts", func(t *testing.T) {
		require.NotEqual(t, native, platformTestDigest(t, platformTestBuildOutput(amd64, "amd64", "compile", nil), arm64))
		require.NotEqual(t, native, platformTestDigest(t, platformTestBuildOutput(amd64, "arm64", "link", nil), arm64))
	})
	t.Run("every platform a command runs on is left out", func(t *testing.T) {
		// Whatever engine made the container a command ran in, such as an
		// equivalent result another engine's image shipped.
		require.Equal(t, native, platformTestDigest(t, platformTestBuildOutput(amd64, "arm64", "compile", nil), arm64))
	})
	t.Run("a string that isn't a platform argument is kept", func(t *testing.T) {
		require.NotEqual(t,
			platformTestDigest(t, platformTestBuildOutput(arm64, arm64, "compile", nil), arm64),
			platformTestDigest(t, platformTestBuildOutput(amd64, amd64, "compile", nil), arm64))
	})
	t.Run("the engine-default-platform input is left out", func(t *testing.T) {
		// Whatever engine made the call, such as an equivalent result
		// another engine's image shipped.
		dir := func(platform string) *ResultCall {
			frame := platformTestCall("Directory", "directory", nil)
			frame.ImplicitInputs = []*ResultCallArg{platformTestString("engineDefaultPlatform", platform)}
			return platformTestCall("Directory", "withNewFile", frame, platformTestString("path", "a"))
		}
		require.Equal(t, platformTestDigest(t, dir(arm64), arm64), platformTestDigest(t, dir(amd64), arm64))
	})
	t.Run("an input without the build platform is its content", func(t *testing.T) {
		lib := func(recipe string) *ResultCall {
			return cacheTestIntCall(recipe, call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: digest.FromString("lib content")})
		}
		require.Equal(t,
			platformTestDigest(t, platformTestBuildOutput(arm64, "arm64", "compile", lib("session-a")), arm64),
			platformTestDigest(t, platformTestBuildOutput(amd64, "arm64", "compile", lib("session-b")), arm64))
	})
	t.Run("an input with the build platform is its recipe, not its content", func(t *testing.T) {
		// A content digest made from a recipe, as a container's from is,
		// names the platform the recipe ran on.
		from := func(platform string) *ResultCall {
			ctr := platformTestCall("Container", "container", nil, platformTestString("platform", platform))
			ctr = platformTestCall("Container", "from", ctr, platformTestString("address", "golang@sha256:index"))
			ctr.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: digest.FromString("from " + platform)}}
			return platformTestCall("Container", "withExec", ctr, platformTestString("args", "compile"))
		}
		require.Equal(t, platformTestDigest(t, from(arm64), arm64), platformTestDigest(t, from(amd64), arm64))
	})
	t.Run("a declared input is its declaration", func(t *testing.T) {
		lib := func(recipe string) *ResultCall {
			return cacheTestIntCall(recipe, call.ExtraDigest{Label: ExtraDigestLabelPlatform, Digest: digest.FromString("declared lib")})
		}
		other := cacheTestIntCall("lib", call.ExtraDigest{Label: ExtraDigestLabelPlatform, Digest: digest.FromString("other lib")})
		withLib := platformTestDigest(t, platformTestBuildOutput(arm64, "arm64", "compile", lib("native")), arm64)
		require.Equal(t, withLib, platformTestDigest(t, platformTestBuildOutput(amd64, "arm64", "compile", lib("cross")), arm64))
		require.NotEqual(t, withLib, platformTestDigest(t, platformTestBuildOutput(arm64, "arm64", "compile", other), arm64))
	})
	t.Run("an input pulled for a platform keeps it", func(t *testing.T) {
		// A target sysroot is the same input on every engine, whether or
		// not the engine is of its platform, with its content or without.
		for _, content := range []bool{false, true} {
			sysroot := func() *ResultCall {
				ctr := platformTestCall("Container", "container", nil, platformTestString("platform", arm64))
				ctr = platformTestCall("Container", "from", ctr, platformTestString("address", "sysroot@sha256:index"))
				if content {
					ctr.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: digest.FromString("from sysroot " + arm64)}}
				}
				return platformTestCall("Directory", "directory", ctr, platformTestString("path", "/usr/lib"))
			}
			require.Equal(t,
				platformTestDigest(t, platformTestBuildOutputFrom(arm64, "arm64", "compile", sysroot(), true), arm64),
				platformTestDigest(t, platformTestBuildOutputFrom(amd64, "arm64", "compile", sysroot(), true), arm64))
		}
		// An input pulled for another platform is another input.
		other := platformTestCall("Container", "container", nil, platformTestString("platform", amd64))
		other = platformTestCall("Container", "from", other, platformTestString("address", "sysroot@sha256:index"))
		other = platformTestCall("Directory", "directory", other, platformTestString("path", "/usr/lib"))
		arm := platformTestCall("Container", "container", nil, platformTestString("platform", arm64))
		arm = platformTestCall("Container", "from", arm, platformTestString("address", "sysroot@sha256:index"))
		arm = platformTestCall("Directory", "directory", arm, platformTestString("path", "/usr/lib"))
		require.NotEqual(t,
			platformTestDigest(t, platformTestBuildOutputFrom(amd64, "arm64", "compile", arm, true), arm64),
			platformTestDigest(t, platformTestBuildOutputFrom(amd64, "arm64", "compile", other, true), arm64))
	})
	t.Run("a command's output read as an input is made on the build platform", func(t *testing.T) {
		// A tool built on the engine and mounted into the build.
		tool := func(platform string) *ResultCall {
			ctr := platformTestCall("Container", "container", nil, platformTestString("platform", platform))
			ctr = platformTestCall("Container", "from", ctr, platformTestString("address", "golang@sha256:index"))
			ctr = platformTestCall("Container", "withExec", ctr, platformTestString("args", "go build tool"))
			return platformTestCall("Directory", "directory", ctr, platformTestString("path", "/tool"))
		}
		require.Equal(t,
			platformTestDigest(t, platformTestBuildOutputFrom(arm64, "arm64", "compile", tool(arm64), true), arm64),
			platformTestDigest(t, platformTestBuildOutputFrom(amd64, "arm64", "compile", tool(amd64), true), arm64))
	})
}
