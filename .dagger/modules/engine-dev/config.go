package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"golang.org/x/exp/maps"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
)

const (
	engineTOMLPath       = "/etc/dagger/engine.toml"
	engineJSONPath       = "/etc/dagger/engine.json"
	engineEntrypointPath = "/usr/local/bin/dagger-entrypoint.sh"
	cliPath              = consts.DaggerCLIPath
)

const engineEntrypointTmpl = `#!/bin/sh
set -e

cat $0

# cgroup v2: enable nesting
# see https://github.com/moby/moby/blob/38805f20f9bcc5e87869d6c79d432b166e1c88b4/hack/dind#L28
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
	mkdir -p /sys/fs/cgroup/engine
	controllers=$(sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers)
	# Retry, as moby does since https://github.com/moby/moby/commit/c43aa0b6aa7c88343f0951ba9a39c69aa51c54ef:
	# a process that joins the root group after the move makes the write fail.
	# A client connecting through "docker exec" while the engine starts does:
	# runc puts it in the root group while no controllers are enabled there.
	attempt=1
	until
		# move the processes from the root group to the /engine group,
		# otherwise writing subtree_control fails with EBUSY.
		# An error during moving non-existent process (i.e., "cat") is ignored.
		# One pid per write: cgroup.procs migrates only the first pid of a write,
		# and busybox xargs batches its echo output into one write.
		for pid in $(cat /sys/fs/cgroup/cgroup.procs); do
			echo "$pid" > /sys/fs/cgroup/engine/cgroup.procs 2>/dev/null || :
		done
		# enable controllers; unlike sed, echo reports why a write failed
		echo "$controllers" > /sys/fs/cgroup/cgroup.subtree_control
	do
		if [ "$attempt" -ge 10 ]; then
			echo "failed to enable cgroup controllers after $attempt attempts" >&2
			for f in cgroup.type cgroup.controllers cgroup.subtree_control; do
				echo "$f: $(cat "/sys/fs/cgroup/$f")" >&2
			done
			for pid in $(head -n 20 /sys/fs/cgroup/cgroup.procs); do
				echo "still in root group: $pid $(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null)" >&2
			done
			exit 1
		fi
		attempt=$((attempt + 1))
		sleep 0.1
	done
	if [ "$attempt" -gt 1 ]; then
		echo "enabled cgroup controllers on attempt $attempt"
	fi
fi

# expect more open files due to per-client SQLite databases
# many systems default to 1024 which is far too low
ulimit -n 1048576 || echo "cannot increase open FDs with ulimit, ignoring"

exec tini -- {{.EngineBin}} --config {{.EngineConfig}} "$@"
`

const engineConfigTmpl = `
{{ range $key := .ConfigKeys }}
[{{ $key }}]
{{ index $.ConfigEntries $key }}
{{ end -}}
`

func generateEntrypoint() (*dagger.File, error) {
	type entrypointTmplParams struct {
		Bridge            string
		EngineBin         string
		EngineConfig      string
		EntrypointArgs    map[string]string
		EntrypointArgKeys []string
	}
	tmpl := template.Must(template.New("entrypoint").Parse(engineEntrypointTmpl))
	buf := new(bytes.Buffer)
	err := tmpl.Execute(buf, entrypointTmplParams{
		EngineBin:    consts.EngineServerPath,
		EngineConfig: engineTOMLPath,
	})
	if err != nil {
		return nil, err
	}

	entrypoint := dag.Directory().
		WithNewFile("dagger-entrypoint.sh", buf.String(), dagger.DirectoryWithNewFileOpts{
			Permissions: 0o755,
		}).
		File("dagger-entrypoint.sh")
	return entrypoint, nil
}

func generateConfig(logLevel string) (*dagger.File, error) {
	cfg := struct {
		LogLevel   string `json:"logLevel,omitempty"`
		Registries map[string]struct {
			Mirrors []string `json:"mirrors"`
		} `json:"registries"`
	}{
		LogLevel: logLevel,
		Registries: map[string]struct {
			Mirrors []string `json:"mirrors"`
		}{
			"docker.io": {
				Mirrors: []string{"mirror.gcr.io"},
			},
		},
	}

	res, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}

	filename := filepath.Base(engineJSONPath)
	config := dag.Directory().
		WithNewFile(filename, string(res)).
		File(filename)
	return config, nil
}

func generateEngineTOML(kvs []string) (*dagger.File, error) {
	opts := map[string]string{}
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("no value for key %q", k)
		}
		opts[k] = v
	}
	keys := maps.Keys(opts)
	sort.Strings(keys)

	type configTmplParams struct {
		Trace         bool
		ConfigEntries map[string]string
		ConfigKeys    []string
	}
	tmpl := template.Must(template.New("config").Parse(engineConfigTmpl))
	buf := new(bytes.Buffer)
	err := tmpl.Execute(buf, configTmplParams{
		ConfigEntries: opts,
		ConfigKeys:    keys,
	})
	if err != nil {
		return nil, err
	}

	filename := filepath.Base(engineTOMLPath)
	config := dag.Directory().
		WithNewFile(filename, buf.String()).
		File(filename)
	return config, nil
}
