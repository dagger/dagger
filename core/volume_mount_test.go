package core

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/slog"
)

func TestPrepareEngineVolumeSourceCreatesAndPreservesDirectories(t *testing.T) {
	t.Parallel()

	engineRoot := t.TempDir()
	existing := filepath.Join(engineRoot, "volumes", "v1", "group")
	require.NoError(t, os.MkdirAll(existing, 0o750))
	require.NoError(t, os.Chmod(existing, 0o750))

	source, err := prepareEngineVolumeSource(engineRoot, &EngineVolumeConfig{
		Name:          "group/data",
		LayoutVersion: EngineVolumeLayoutVersion,
	})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(existing, "data", "fs"), source)

	info, err := os.Stat(source)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	existingInfo, err := os.Stat(existing)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o750), existingInfo.Mode().Perm())
}

func TestPrepareEngineVolumeSourceConcurrentCreation(t *testing.T) {
	t.Parallel()

	engineRoot := t.TempDir()
	cfg := &EngineVolumeConfig{Name: "concurrent/data", LayoutVersion: EngineVolumeLayoutVersion}
	const callers = 16
	results := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			source, err := prepareEngineVolumeSource(engineRoot, cfg)
			results <- source
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	want := filepath.Join(engineRoot, "volumes", "v1", "concurrent", "data", "fs")
	for result := range results {
		require.Equal(t, want, result)
	}
	info, err := os.Stat(want)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestPrepareEngineVolumeSourceSubdirAndSymlinkClamp(t *testing.T) {
	t.Parallel()

	engineRoot := t.TempDir()
	cfg := &EngineVolumeConfig{Name: "safe/data", LayoutVersion: EngineVolumeLayoutVersion}
	volumeRoot, err := prepareEngineVolumeSource(engineRoot, cfg)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(volumeRoot, "existing"), 0o755))

	cfg.Subdir = "existing"
	selected, err := prepareEngineVolumeSource(engineRoot, cfg)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(volumeRoot, "existing"), selected)

	cfg.Subdir = "missing"
	_, err = prepareEngineVolumeSource(engineRoot, cfg)
	require.ErrorContains(t, err, "does not exist")
	require.NoError(t, os.WriteFile(filepath.Join(volumeRoot, "file"), []byte("payload"), 0o644))
	cfg.Subdir = "file"
	_, err = prepareEngineVolumeSource(engineRoot, cfg)
	require.ErrorContains(t, err, "is not a directory")

	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(volumeRoot, "escape")))
	cfg.Subdir = "escape"
	selected, err = prepareEngineVolumeSource(engineRoot, cfg)
	require.Error(t, err)
	require.Empty(t, selected)
	require.NotContains(t, err.Error(), outside)
}

func TestPrepareEngineVolumeSourceRejectsWrongType(t *testing.T) {
	t.Parallel()

	engineRoot := t.TempDir()
	wrongType := filepath.Join(engineRoot, "volumes")
	require.NoError(t, os.WriteFile(wrongType, []byte("operator file"), 0o640))
	_, err := prepareEngineVolumeSource(engineRoot, &EngineVolumeConfig{
		Name:          "data",
		LayoutVersion: EngineVolumeLayoutVersion,
	})
	require.ErrorContains(t, err, "not a directory")
	contents, readErr := os.ReadFile(wrongType)
	require.NoError(t, readErr)
	require.Equal(t, "operator file", string(contents))
}

func TestEngineVolumeMountOptionsFollowCapability(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		readonly  bool
		supported bool
		options   []string
	}{
		{name: "writable", options: []string{"rbind"}},
		{name: "recursive readonly", readonly: true, supported: true, options: []string{"rbind", "rro"}},
		{name: "readonly fallback", readonly: true, options: []string{"rbind", "ro"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mountable := &execEngineVolumeMount{
				cfg:   EngineVolumeConfig{Name: "data", LayoutVersion: EngineVolumeLayoutVersion},
				state: EngineVolumeState{RootDir: root, RecursiveReadOnlySupported: tc.supported},
			}
			ref, err := mountable.Mount(context.Background(), tc.readonly)
			require.NoError(t, err)
			mounts, release, err := ref.Mount()
			require.NoError(t, err)
			require.NotNil(t, release)
			require.NoError(t, release())
			require.Len(t, mounts, 1)
			require.Equal(t, tc.options, mounts[0].Options)
		})
	}
}

func TestSSHFSCommandArgsSecure(t *testing.T) {
	t.Parallel()

	args := sshfsCommandArgs("git@example.com:/srv/repo", "/tmp/mnt", sshfsCommandConfig{
		Port:           "2222",
		PrivateKeyPath: "/tmp/key",
		KnownHostsPath: "/tmp/known_hosts",
		HostKeyAlias:   "[example.com]:2222",
		AllowOther:     true,
		Readonly:       true,
	})

	require.Equal(t, []string{
		"git@example.com:/srv/repo",
		"/tmp/mnt",
		"-p", "2222",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityFile=/tmp/key",
		"-o", "allow_other",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=/tmp/known_hosts",
		"-o", "HostKeyAlias=[example.com]:2222",
		"-o", "ro",
	}, args)
}

func TestSSHFSCommandArgsInsecure(t *testing.T) {
	t.Parallel()

	args := sshfsCommandArgs("git@example.com:/srv/repo", "/tmp/mnt", sshfsCommandConfig{
		PrivateKeyPath:           "/tmp/key",
		KnownHostsPath:           "/tmp/known_hosts",
		HostKeyAlias:             "example.com",
		InsecureSkipHostKeyCheck: true,
		AllowOther:               true,
	})

	require.Equal(t, []string{
		"git@example.com:/srv/repo",
		"/tmp/mnt",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityFile=/tmp/key",
		"-o", "allow_other",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}, args)
}

func TestSSHFSCommandArgsWithoutAllowOther(t *testing.T) {
	t.Parallel()

	args := sshfsCommandArgs("git@example.com:/srv/repo", "/tmp/mnt", sshfsCommandConfig{
		PrivateKeyPath: "/tmp/key",
	})

	require.NotContains(t, args, "allow_other")
}

func TestFuseConfAllowsOther(t *testing.T) {
	t.Parallel()

	require.True(t, fuseConfAllowsOther([]byte(`
# comment
user_allow_other
`)))
	require.True(t, fuseConfAllowsOther([]byte(" user_allow_other # trailing comment\n")))
	require.False(t, fuseConfAllowsOther([]byte("# user_allow_other\n")))
	require.False(t, fuseConfAllowsOther([]byte("mount_max = 1000\n")))
}

func TestSSHFSServiceHostPort(t *testing.T) {
	t.Parallel()

	port, err := sshfsServiceHostPort(&RunningService{
		Ports: []Port{{Port: 2222, Protocol: NetworkProtocolTCP}},
	}, "")
	require.NoError(t, err)
	require.Equal(t, "2222", port)

	port, err = sshfsServiceHostPort(&RunningService{
		Ports: []Port{
			{Port: 2222, Protocol: NetworkProtocolTCP},
			{Port: 8080, Protocol: NetworkProtocolTCP},
		},
	}, "2222")
	require.NoError(t, err)
	require.Equal(t, "2222", port)
}

func TestSSHFSServiceHostPortRejectsAmbiguousService(t *testing.T) {
	t.Parallel()

	_, err := sshfsServiceHostPort(&RunningService{}, "")
	require.ErrorContains(t, err, "exposes no ports")

	_, err = sshfsServiceHostPort(&RunningService{
		Ports: []Port{
			{Port: 2222, Protocol: NetworkProtocolTCP},
			{Port: 8080, Protocol: NetworkProtocolTCP},
		},
	}, "")
	require.ErrorContains(t, err, "multiple TCP ports")

	_, err = sshfsServiceHostPort(&RunningService{
		Ports: []Port{{Port: 2222, Protocol: NetworkProtocolTCP}},
	}, "2022")
	require.ErrorContains(t, err, "does not expose endpoint port 2022")
}

func TestSSHFSSourceForURL(t *testing.T) {
	t.Parallel()

	endpoint, err := url.Parse("sshfs://git@example.com/srv/repo")
	require.NoError(t, err)
	source, err := sshfsSourceForURL(endpoint, endpoint.Hostname())
	require.NoError(t, err)
	require.Equal(t, "git@example.com:/srv/repo", source)

	endpoint, err = url.Parse("sshfs://git@[::1]/srv/repo")
	require.NoError(t, err)
	source, err = sshfsSourceForURL(endpoint, endpoint.Hostname())
	require.NoError(t, err)
	require.Equal(t, "git@[::1]:/srv/repo", source)
}

func TestSSHFSSourceForURLRejectsInvalidEndpoint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		endpoint string
		host     string
		wantErr  string
	}{
		{
			name:     "missing user",
			endpoint: "sshfs://example.com/srv/repo",
			host:     "example.com",
			wantErr:  "missing user",
		},
		{
			name:     "missing host",
			endpoint: "sshfs://git@example.com/srv/repo",
			wantErr:  "missing host",
		},
		{
			name:     "missing path",
			endpoint: "sshfs://git@example.com",
			host:     "example.com",
			wantErr:  "missing absolute path",
		},
		{
			name:     "leading dash host",
			endpoint: "sshfs://git@example.com/srv/repo",
			host:     "-example.com",
			wantErr:  "must not start with '-'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint, err := url.Parse(tc.endpoint)
			require.NoError(t, err)
			_, err = sshfsSourceForURL(endpoint, tc.host)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRunSSHFSVolumeCleanupLogsError(t *testing.T) {
	var logs bytes.Buffer
	ctx := slog.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)))
	wantErr := errors.New("cleanup failed")

	err := runSSHFSVolumeCleanup(ctx, func() error {
		return wantErr
	})

	require.ErrorIs(t, err, wantErr)
	require.Contains(t, logs.String(), "failed to clean up SSHFS volume")
	require.Contains(t, logs.String(), wantErr.Error())
}
