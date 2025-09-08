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

	"github.com/dagger/dagger/cmd/engine/.dagger/consts"
	"github.com/dagger/dagger/cmd/engine/.dagger/internal/dagger"
)

const (
	engineTOMLPath       = "/etc/dagger/engine.toml"
	engineJSONPath       = "/etc/dagger/engine.json"
	engineEntrypointPath = "/usr/local/bin/dagger-entrypoint.sh"
	cliPath              = "/usr/local/bin/dagger"
)

const engineEntrypointTmpl = `#!/bin/sh
set -e

cat $0

# cgroup v2: enable nesting
# see https://github.com/moby/moby/blob/38805f20f9bcc5e87869d6c79d432b166e1c88b4/hack/dind#L28
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
	# move the processes from the root group to the /init group,
	# otherwise writing subtree_control fails with EBUSY.
	# An error during moving nonexistent process (i.e., "cat") is ignored.
	mkdir -p /sys/fs/cgroup/init
	xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs || :
	# enable controllers
	sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers \
		> /sys/fs/cgroup/cgroup.subtree_control
fi

# expect more open files due to per-client SQLite databases
# many systems default to 1024 which is far too low
ulimit -n 1048576 || echo "cannot increase open FDs with ulimit, ignoring"

exec {{.EngineBin}} --config {{.EngineConfig}} "$@"
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
		LogLevel string `json:"logLevel,omitempty"`
	}{
		LogLevel: logLevel,
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

func generateBKConfig(kvs []string) (*dagger.File, error) {
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
