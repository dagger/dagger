package dockerfile2llb

import (
	"context"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/util/appcontext"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
)

func toEnvMap(args []instructions.KeyValuePairOptional, env []string) map[string]string {
	m := shell.BuildEnvs(env)

	for _, arg := range args {
		// If key already exists, keep previous value.
		if _, ok := m[arg.Key]; ok {
			continue
		}
		if arg.Value != nil {
			m[arg.Key] = arg.ValueString()
		}
	}
	return m
}

func TestDockerfileParsing(t *testing.T) {
	t.Parallel()
	df := `FROM scratch
ENV FOO bar
COPY f1 f2 /sub/
RUN ls -l
`
	_, _, _, _, err := Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.NoError(t, err)

	df = `FROM scratch AS foo
ENV FOO bar
FROM foo
COPY --from=foo f1 /
COPY --from=0 f2 /
	`
	_, _, _, _, err = Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.NoError(t, err)

	df = `FROM scratch AS foo
ENV FOO bar
FROM foo
COPY --from=foo f1 /
COPY --from=0 f2 /
	`
	_, _, _, _, err = Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{
		Config: dockerui.Config{
			Target: "Foo",
		},
	})
	assert.NoError(t, err)

	_, _, _, _, err = Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{
		Config: dockerui.Config{
			Target: "nosuch",
		},
	})
	assert.Error(t, err)

	df = `FROM scratch
	ADD http://github.com/moby/buildkit/blob/master/README.md /
		`
	_, _, _, _, err = Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.NoError(t, err)

	df = `FROM scratch
	COPY http://github.com/moby/buildkit/blob/master/README.md /
		`
	_, _, _, _, err = Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.EqualError(t, err, "source can't be a URL for COPY")

	df = `FROM "" AS foo`
	_, _, _, _, err = Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.Error(t, err)

	df = `FROM ${BLANK} AS foo`
	_, _, _, _, err = Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.Error(t, err)
}

func TestDockerfileParsingMarshal(t *testing.T) {
	t.Parallel()
	df := `FROM scratch
ENV FOO bar
COPY f1 f2 /sub/
RUN ls -l
`
	state, _, _, _, err := Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.NoError(t, err)

	_, err = state.Marshal(context.TODO())
	assert.NoError(t, err)
}

func TestAddEnv(t *testing.T) {
	// k exists in env as key
	// override = true
	env := []string{"key1=val1", "key2=val2"}
	result := addEnv(env, "key1", "value1")
	assert.Equal(t, []string{"key1=value1", "key2=val2"}, result)

	// k does not exist in env as key
	// override = true
	env = []string{"key1=val1", "key2=val2"}
	result = addEnv(env, "key3", "val3")
	assert.Equal(t, []string{"key1=val1", "key2=val2", "key3=val3"}, result)

	// env has same keys
	// override = true
	env = []string{"key1=val1", "key1=val2"}
	result = addEnv(env, "key1", "value1")
	assert.Equal(t, []string{"key1=value1", "key1=val2"}, result)

	// k matches with key only string in env
	// override = true
	env = []string{"key1=val1", "key2=val2", "key3"}
	result = addEnv(env, "key3", "val3")
	assert.Equal(t, []string{"key1=val1", "key2=val2", "key3=val3"}, result)
}

func TestParseKeyValue(t *testing.T) {
	k, v := parseKeyValue("key=val")
	assert.Equal(t, "key", k)
	assert.Equal(t, "val", v)

	k, v = parseKeyValue("key=")
	assert.Equal(t, "key", k)
	assert.Equal(t, "", v)

	k, v = parseKeyValue("key")
	assert.Equal(t, "key", k)
	assert.Equal(t, "", v)
}

func TestToEnvList(t *testing.T) {
	// args has no duplicated key with env
	v := "val2"
	args := []instructions.KeyValuePairOptional{{Key: "key2", Value: &v}}
	env := []string{"key1=val1"}
	resutl := toEnvMap(args, env)
	assert.Equal(t, map[string]string{"key1": "val1", "key2": "val2"}, resutl)

	// value of args is nil
	args = []instructions.KeyValuePairOptional{{Key: "key2", Value: nil}}
	env = []string{"key1=val1"}
	resutl = toEnvMap(args, env)
	assert.Equal(t, map[string]string{"key1": "val1"}, resutl)

	// args has duplicated key with env
	v = "val2"
	args = []instructions.KeyValuePairOptional{{Key: "key1", Value: &v}}
	env = []string{"key1=val1"}
	resutl = toEnvMap(args, env)
	assert.Equal(t, map[string]string{"key1": "val1"}, resutl)

	v = "val2"
	args = []instructions.KeyValuePairOptional{{Key: "key1", Value: &v}}
	env = []string{"key1="}
	resutl = toEnvMap(args, env)
	assert.Equal(t, map[string]string{"key1": ""}, resutl)

	v = "val2"
	args = []instructions.KeyValuePairOptional{{Key: "key1", Value: &v}}
	env = []string{"key1"}
	resutl = toEnvMap(args, env)
	assert.Equal(t, map[string]string{"key1": ""}, resutl)

	// env has duplicated keys
	v = "val2"
	args = []instructions.KeyValuePairOptional{{Key: "key2", Value: &v}}
	env = []string{"key1=val1", "key1=val1_2"}
	resutl = toEnvMap(args, env)
	assert.Equal(t, map[string]string{"key1": "val1_2", "key2": "val2"}, resutl)

	// args has duplicated keys
	v1 := "v1"
	v2 := "v2"
	args = []instructions.KeyValuePairOptional{{Key: "key2", Value: &v1}, {Key: "key2", Value: &v2}}
	env = []string{"key1=val1"}
	resutl = toEnvMap(args, env)
	assert.Equal(t, map[string]string{"key1": "val1", "key2": "v1"}, resutl)
}

func TestDockerfileCircularDependencies(t *testing.T) {
	// single stage depends on itself
	df := `FROM busybox AS stage0
COPY --from=stage0 f1 /sub/
`
	_, _, _, _, err := Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.EqualError(t, err, "circular dependency detected on stage: stage0")

	// multiple stages with circular dependency
	df = `FROM busybox AS stage0
COPY --from=stage2 f1 /sub/
FROM busybox AS stage1
COPY --from=stage0 f2 /sub/
FROM busybox AS stage2
COPY --from=stage1 f2 /sub/
`
	_, _, _, _, err = Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.EqualError(t, err, "circular dependency detected on stage: stage0")
}

func TestBaseImageConfig(t *testing.T) {
	df := `FROM --platform=linux/amd64 busybox:1.36.1@sha256:6d9ac9237a84afe1516540f40a0fafdc86859b2141954b4d643af7066d598b74 AS foo
RUN echo foo

# the source image of bar is busybox, not foo
FROM foo AS bar
RUN echo bar
`
	_, _, baseImg, _, err := Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.NoError(t, err)
	t.Logf("baseImg=%+v", baseImg)
	assert.Equal(t, []digest.Digest{"sha256:2e112031b4b923a873c8b3d685d48037e4d5ccd967b658743d93a6e56c3064b9"}, baseImg.RootFS.DiffIDs)
	assert.Equal(t, "2024-01-17 21:49:12 +0000 UTC", baseImg.Created.String())
}
