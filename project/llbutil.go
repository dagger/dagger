package project

import (
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/util/sshutil"
)

type runOptionFunc func(*llb.ExecInfo)

func (fn runOptionFunc) SetRunOption(ei *llb.ExecInfo) {
	if fn != nil {
		fn(ei)
	}
}

func withRunOpts(runOpts ...llb.RunOption) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		for _, runOpt := range runOpts {
			runOpt.SetRunOption(ei)
		}
	})
}

func withSSHAuthSock(id, path string) llb.RunOption {
	if id == "" {
		return runOptionFunc(nil)
	}
	return withRunOpts(
		llb.AddSSHSocket(
			llb.SSHID(id),
			llb.SSHSocketTarget(path),
		),
		llb.AddEnv("SSH_AUTH_SOCK", path),
	)
}

func withGithubSSHKnownHosts() (llb.RunOption, error) {
	knownHosts, err := sshutil.SSHKeyScan("github.com")
	if err != nil {
		return nil, err
	}

	return withRunOpts(
		llb.AddMount("/tmp/known_hosts",
			llb.Scratch().File(llb.Mkfile("known_hosts", 0600, []byte(knownHosts))),
			llb.SourcePath("/known_hosts"),
			llb.ForceNoOutput,
		),
		llb.AddEnv("GIT_SSH_COMMAND", "ssh -o UserKnownHostsFile=/tmp/known_hosts"),
	), nil
}

func shell(lines ...string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", strings.Join(lines, "\n")})
}
