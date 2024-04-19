package dockerfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var hdTests = integration.TestFuncs(
	testCopyHeredoc,
	testCopyHeredocSpecialSymbols,
	testRunBasicHeredoc,
	testRunFakeHeredoc,
	testRunShebangHeredoc,
	testRunComplexHeredoc,
	testHeredocIndent,
	testHeredocVarSubstitution,
	testOnBuildHeredoc,
)

func init() {
	heredocTests = append(heredocTests, hdTests...)
}

func testCopyHeredoc(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build

RUN adduser -D user
WORKDIR /dest

COPY <<EOF single
single file
EOF

COPY <<EOF <<EOF2 double/
first file
EOF
second file
EOF2

RUN mkdir -p /permfiles
COPY --chmod=777 <<EOF /permfiles/all
dummy content
EOF
COPY --chmod=0644 <<EOF /permfiles/rw
dummy content
EOF
COPY --chown=user:user <<EOF /permfiles/owned
dummy content
EOF
RUN stat -c "%04a" /permfiles/all >> perms && \
	stat -c "%04a" /permfiles/rw >> perms && \
	stat -c "%U:%G" /permfiles/owned >> perms

FROM scratch
COPY --from=build /dest /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	contents := map[string]string{
		"single":      "single file\n",
		"double/EOF":  "first file\n",
		"double/EOF2": "second file\n",
		"perms":       "0777\n0644\nuser:user\n",
	}

	for name, content := range contents {
		dt, err := os.ReadFile(filepath.Join(destDir, name))
		require.NoError(t, err)
		require.Equal(t, content, string(dt))
	}
}

func testCopyHeredocSpecialSymbols(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM scratch

COPY <<EOF quotefile
"quotes in file"
EOF

COPY <<EOF slashfile1
\
EOF
COPY <<EOF slashfile2
\\
EOF
COPY <<EOF slashfile3
\$
EOF

COPY <<"EOF" rawslashfile1
\
EOF
COPY <<"EOF" rawslashfile2
\\
EOF
COPY <<"EOF" rawslashfile3
\$
EOF
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "quotefile"))
	require.NoError(t, err)
	require.Equal(t, "\"quotes in file\"\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "slashfile1"))
	require.NoError(t, err)
	require.Equal(t, "\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "slashfile2"))
	require.NoError(t, err)
	require.Equal(t, "\\\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "slashfile3"))
	require.NoError(t, err)
	require.Equal(t, "$\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "rawslashfile1"))
	require.NoError(t, err)
	require.Equal(t, "\\\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "rawslashfile2"))
	require.NoError(t, err)
	require.Equal(t, "\\\\\n", string(dt))

	dt, err = os.ReadFile(filepath.Join(destDir, "rawslashfile3"))
	require.NoError(t, err)
	require.Equal(t, "\\$\n", string(dt))
}

func testRunBasicHeredoc(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build

RUN <<EOF
echo "i am" >> /dest
whoami >> /dest
EOF

FROM scratch
COPY --from=build /dest /dest
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "dest"))
	require.NoError(t, err)
	require.Equal(t, "i am\nroot\n", string(dt))
}

func testRunFakeHeredoc(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build

SHELL ["/bin/awk"]
RUN <<EOF
BEGIN {
	print "foo" > "/dest"
}
EOF

FROM scratch
COPY --from=build /dest /dest
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "dest"))
	require.NoError(t, err)
	require.Equal(t, "foo\n", string(dt))
}

func testRunShebangHeredoc(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build

RUN <<EOF
#!/bin/awk -f
BEGIN {
	print "hello" >> "/dest"
	print "world" >> "/dest"
}
EOF

FROM scratch
COPY --from=build /dest /dest
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "dest"))
	require.NoError(t, err)
	require.Equal(t, "hello\nworld\n", string(dt))
}

func testRunComplexHeredoc(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build

WORKDIR /dest

RUN cat <<EOF1 | tr '[:upper:]' '[:lower:]' > ./out1; \
	cat <<EOF2 | tr '[:lower:]' '[:upper:]' > ./out2
hello WORLD
EOF1
HELLO world
EOF2

RUN <<EOF 3<<IN1 4<<IN2 awk -f -
BEGIN {
	while ((getline line < "/proc/self/fd/3") > 0)
		print tolower(line) > "./fd3"
	while ((getline line < "/proc/self/fd/4") > 0)
		print toupper(line) > "./fd4"
}
EOF
hello WORLD
IN1
HELLO world
IN2

FROM scratch
COPY --from=build /dest /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	contents := map[string]string{
		"out1": "hello world\n",
		"out2": "HELLO WORLD\n",
		"fd3":  "hello world\n",
		"fd4":  "HELLO WORLD\n",
	}

	for name, content := range contents {
		dt, err := os.ReadFile(filepath.Join(destDir, name))
		require.NoError(t, err)
		require.Equal(t, content, string(dt))
	}
}

func testHeredocIndent(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox AS build

COPY <<EOF /dest/foo-copy
	foo
EOF

COPY <<-EOF /dest/bar-copy
	bar
EOF

RUN <<EOF
echo "
	foo" > /dest/foo-run
EOF

RUN <<-EOF
echo "
	bar" > /dest/bar-run
EOF

RUN <<EOF
#!/bin/sh
echo "
	foo" > /dest/foo2-run
EOF

RUN <<-EOF
#!/bin/sh
echo "
	bar" > /dest/bar2-run
EOF

RUN <<EOF sh > /dest/foo3-run
echo "
	foo"
EOF

RUN <<-EOF sh > /dest/bar3-run
echo "
	bar"
EOF

FROM scratch
COPY --from=build /dest /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	contents := map[string]string{
		"foo-copy": "\tfoo\n",
		"foo-run":  "\n\tfoo\n",
		"foo2-run": "\n\tfoo\n",
		"foo3-run": "\n\tfoo\n",
		"bar-copy": "bar\n",
		"bar-run":  "\nbar\n",
		"bar2-run": "\nbar\n",
		"bar3-run": "\nbar\n",
	}

	for name, content := range contents {
		dt, err := os.ReadFile(filepath.Join(destDir, name))
		require.NoError(t, err)
		require.Equal(t, content, string(dt))
	}
}

func testHeredocVarSubstitution(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	dockerfile := []byte(`
FROM busybox as build

ARG name=world

COPY <<EOF /dest/c1
Hello ${name}!
EOF
COPY <<'EOF' /dest/c2
Hello ${name}!
EOF
COPY <<"EOF" /dest/c3
Hello ${name}!
EOF

COPY <<EOF /dest/q1
Hello '${name}'!
EOF
COPY <<EOF /dest/q2
Hello "${name}"!
EOF
COPY <<'EOF' /dest/qsingle1
Hello '${name}'!
EOF
COPY <<'EOF' /dest/qsingle2
Hello "${name}"!
EOF
COPY <<"EOF" /dest/qdouble1
Hello '${name}'!
EOF
COPY <<"EOF" /dest/qdouble2
Hello "${name}"!
EOF

RUN <<EOF
greeting="Hello"
echo "${greeting} ${name}!" > /dest/r1
EOF
RUN <<EOF
name="new world"
echo "Hello ${name}!" > /dest/r2
EOF

FROM scratch
COPY --from=build /dest /
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", []byte(dockerfile), 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	contents := map[string]string{
		"c1":       "Hello world!\n",
		"c2":       "Hello ${name}!\n",
		"c3":       "Hello ${name}!\n",
		"q1":       "Hello 'world'!\n",
		"q2":       "Hello \"world\"!\n",
		"qsingle1": "Hello '${name}'!\n",
		"qsingle2": "Hello \"${name}\"!\n",
		"qdouble1": "Hello '${name}'!\n",
		"qdouble2": "Hello \"${name}\"!\n",
		"r1":       "Hello world!\n",
		"r2":       "Hello new world!\n",
	}

	for name, content := range contents {
		dt, err := os.ReadFile(filepath.Join(destDir, name))
		require.NoError(t, err)
		require.Equal(t, content, string(dt))
	}
}

func testOnBuildHeredoc(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureDirectPush)
	f := getFrontend(t, sb)

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)

	dockerfile := []byte(`
FROM busybox
ONBUILD RUN <<EOF
echo "hello world" >> /dest
EOF
`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	c, err := client.New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	target := registry + "/buildkit/testonbuildheredoc:base"
	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"push": "true",
					"name": target,
				},
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dockerfile = []byte(fmt.Sprintf(`
	FROM %s AS base
	FROM scratch
	COPY --from=base /dest /dest
	`, target))

	dir = integration.Tmpdir(
		t,
		fstest.CreateFile("Dockerfile", dockerfile, 0600),
	)

	destDir := t.TempDir()

	_, err = f.Solve(sb.Context(), c, client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:      client.ExporterLocal,
				OutputDir: destDir,
			},
		},
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: dir,
			dockerui.DefaultLocalNameContext:    dir,
		},
	}, nil)
	require.NoError(t, err)

	dt, err := os.ReadFile(filepath.Join(destDir, "dest"))
	require.NoError(t, err)
	require.Equal(t, "hello world\n", string(dt))
}
