package llbtodagger

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/dockerfile2llb"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestDefinitionToIDDockerfileFromAndRun(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19
RUN echo hello
`)

	fields := fieldsFromRoot(id)
	require.GreaterOrEqual(t, len(fields), 4)
	require.Equal(t, "container", fields[0])
	require.Equal(t, "withExec", fields[len(fields)-2])
	require.Equal(t, "rootfs", fields[len(fields)-1])

	withRootfs := findFieldInChain(id, "withRootfs")
	require.NotNil(t, withRootfs)
	rootfsID := argIDFromCall(t, withRootfs, "directory")
	require.Equal(t, "rootfs", rootfsID.Field())
	fromID := findFieldInChain(rootfsID, "from")
	require.NotNil(t, fromID)
	require.Contains(t, fromID.Arg("address").Value().ToInput(), "docker.io/library/alpine:3.19")

	withExec := findFieldInChain(id, "withExec")
	require.NotNil(t, withExec)
	require.Equal(t, []any{"/bin/sh", "-c", "echo hello"}, withExec.Arg("args").Value().ToInput())
}

func TestDefinitionToIDDockerfileCopyFromContext(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
COPY . /app/
`)

	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(id))
	withDir := id
	require.Equal(t, "/app", withDir.Arg("path").Value().ToInput())

	sourceID := argIDFromCall(t, withDir, "source")
	hostDir := findHostDirectoryCall(sourceID)
	require.NotNil(t, hostDir)
	require.Equal(t, "/workspace", hostDir.Arg("path").Value().ToInput())
}

func TestDefinitionToIDDockerfileAddHTTP(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
ADD https://example.com/pkg.tar.gz /downloads/
`)

	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(id))
	withDir := id
	require.Equal(t, "/downloads", withDir.Arg("path").Value().ToInput())

	sourceID := argIDFromCall(t, withDir, "source")
	httpID := findFieldAnywhere(sourceID, "http")
	require.NotNil(t, httpID)
	require.Equal(t, "https://example.com/pkg.tar.gz", httpID.Arg("url").Value().ToInput())
	require.Equal(t, "pkg.tar.gz", httpID.Arg("name").Value().ToInput())
}

func TestDefinitionToIDDockerfileAddGit(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM scratch
ADD https://github.com/dagger/dagger.git#main /vendor/dagger/
`)

	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(id))
	withDir := id
	require.Equal(t, "/vendor/dagger", withDir.Arg("path").Value().ToInput())

	sourceID := argIDFromCall(t, withDir, "source")
	gitID := findFieldAnywhere(sourceID, "git")
	require.NotNil(t, gitID)
	require.Contains(t, gitID.Arg("url").Value().ToInput(), "github.com/dagger/dagger")

	refID := findFieldAnywhere(sourceID, "ref")
	require.NotNil(t, refID)
	require.Equal(t, "main", refID.Arg("name").Value().ToInput())
}

func TestDefinitionToIDDockerfileCopyLink(t *testing.T) {
	t.Parallel()

	caps := pb.Caps.CapSet(pb.Caps.All())
	id := convertDockerfileToIDWithOpt(t, `
FROM scratch
COPY --link . /linked/
`, func(opt *dockerfile2llb.ConvertOpt) {
		opt.LLBCaps = &caps
	})

	require.Equal(t, []string{"directory", "withDirectory"}, fieldsFromRoot(id))
	copyCall := id
	require.Equal(t, "/linked", copyCall.Arg("path").Value().ToInput())
	require.NotNil(t, findHostDirectoryCall(argIDFromCall(t, copyCall, "source")))
}

func TestDefinitionToIDDockerfileComplexCombination(t *testing.T) {
	t.Parallel()

	id := convertDockerfileToID(t, `
FROM alpine:3.19 AS base
WORKDIR /work
COPY . .
ADD https://example.com/artifact.tgz /work/vendor/
ADD https://github.com/dagger/dagger.git#main /work/vendor/dagger/
RUN echo base

FROM alpine:3.19
WORKDIR /final
COPY --from=base /work /out/work
RUN echo done
`)

	fields := fieldsFromRoot(id)
	require.GreaterOrEqual(t, len(fields), 4)
	require.Equal(t, "container", fields[0])
	require.Equal(t, "withExec", fields[len(fields)-2])
	require.Equal(t, "rootfs", fields[len(fields)-1])

	finalExec := findFieldInChain(id, "withExec")
	require.NotNil(t, finalExec)
	require.Equal(t, []any{"/bin/sh", "-c", "echo done"}, finalExec.Arg("args").Value().ToInput())

	require.NotNil(t, findFieldAnywhere(id, "host"))
	require.NotNil(t, findFieldAnywhere(id, "http"))
	require.NotNil(t, findFieldAnywhere(id, "git"))
	require.NotNil(t, findFieldAnywhere(id, "withNewDirectory"))
}

func TestDefinitionToIDDockerfileDeterministicEncoding(t *testing.T) {
	t.Parallel()

	dockerfile := `
FROM alpine:3.19
WORKDIR /work
COPY . .
RUN echo deterministic
`
	idA := convertDockerfileToID(t, dockerfile)
	idB := convertDockerfileToID(t, dockerfile)

	encA, err := idA.Encode()
	require.NoError(t, err)
	encB, err := idB.Encode()
	require.NoError(t, err)

	require.Equal(t, idA.Digest(), idB.Digest())
	require.Equal(t, encA, encB)
}

func convertDockerfileToID(t *testing.T, dockerfile string) *call.ID {
	t.Helper()
	return convertDockerfileToIDWithOpt(t, dockerfile)
}

func convertDockerfileToIDWithOpt(
	t *testing.T,
	dockerfile string,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) *call.ID {
	t.Helper()

	def := dockerfileToDefinition(t, dockerfile, optFns...)
	id, err := DefinitionToID(def)
	require.NoError(t, err)
	return id
}

func dockerfileToDefinition(
	t *testing.T,
	dockerfile string,
	optFns ...func(*dockerfile2llb.ConvertOpt),
) *pb.Definition {
	t.Helper()

	mainContext := llb.Local("context", llb.SharedKeyHint("/workspace"))
	opt := dockerfile2llb.ConvertOpt{
		MainContext:    &mainContext,
		TargetPlatform: &ocispecs.Platform{OS: "linux", Architecture: "amd64"},
		MetaResolver:   staticImageMetaResolver{},
	}
	for _, fn := range optFns {
		fn(&opt)
	}

	st, _, _, _, err := dockerfile2llb.Dockerfile2LLB(context.Background(), []byte(strings.TrimSpace(dockerfile)), opt)
	require.NoError(t, err)
	require.NotNil(t, st)

	def, err := st.Marshal(context.Background())
	require.NoError(t, err)
	return def.ToPB()
}

type staticImageMetaResolver struct{}

func (staticImageMetaResolver) ResolveImageConfig(context.Context, string, sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	imgCfg := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": []string{digest.FromString("static-layer").String()},
		},
		"config": map[string]any{
			"Env": []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		},
	}
	dt, err := json.Marshal(imgCfg)
	if err != nil {
		return "", "", nil, err
	}
	return "docker.io/library/alpine:3.19", digest.FromString("static-config"), dt, nil
}

func argIDFromCall(t *testing.T, id *call.ID, argName string) *call.ID {
	t.Helper()
	arg := id.Arg(argName)
	require.NotNil(t, arg)
	argID, ok := arg.Value().ToInput().(*call.ID)
	require.True(t, ok)
	require.NotNil(t, argID)
	return argID
}

func findHostDirectoryCall(id *call.ID) *call.ID {
	for cur := id; cur != nil; cur = cur.Receiver() {
		if cur.Field() == "directory" && cur.Receiver() != nil && cur.Receiver().Field() == "host" {
			return cur
		}
	}
	return nil
}

func findFieldAnywhere(root *call.ID, field string) *call.ID {
	seen := map[digest.Digest]struct{}{}
	var visitID func(*call.ID) *call.ID
	var visitAny func(any) *call.ID

	visitAny = func(v any) *call.ID {
		switch x := v.(type) {
		case *call.ID:
			return visitID(x)
		case []any:
			for _, elem := range x {
				if found := visitAny(elem); found != nil {
					return found
				}
			}
		case map[string]any:
			for _, elem := range x {
				if found := visitAny(elem); found != nil {
					return found
				}
			}
		}
		return nil
	}

	visitID = func(id *call.ID) *call.ID {
		if id == nil {
			return nil
		}
		if _, ok := seen[id.Digest()]; ok {
			return nil
		}
		seen[id.Digest()] = struct{}{}

		if id.Field() == field {
			return id
		}
		if found := visitID(id.Receiver()); found != nil {
			return found
		}
		for _, arg := range id.Args() {
			if found := visitAny(arg.Value().ToInput()); found != nil {
				return found
			}
		}
		return nil
	}

	return visitID(root)
}
