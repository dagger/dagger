package gitdns

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/util/progress/logs"
	"github.com/pkg/errors"
)

// gitCLI carries config to pass to the git CLI to make running multiple
// commands less repetitive.
//
// It may also contain references to config files that should be cleaned up
// when the CLI is done being used.
type gitCLI struct {
	gitDir      string   // --git-dir flag
	workTree    string   // --work-tree flag
	sshAuthSock string   // SSH_AUTH_SOCK env value
	knownHosts  string   // file path passed to SSH
	auth        []string // extra auth flags passed to git

	hostsPath  string // generated /etc/hosts from network config
	resolvPath string // generated /etc/resolv.conf from network config
}

// newGitCLI constructs a gitCLI and returns its cleanup function explicitly so
// it's harder to forget to call it.
func newGitCLI(
	gitDir,
	workDir,
	sshAuthSock,
	knownHosts string,
	auth []string,
	dnsConf *oci.DNSConfig,
) (*gitCLI, func(), error) {
	cli := &gitCLI{
		gitDir:      gitDir,
		workTree:    workDir,
		sshAuthSock: sshAuthSock,
		knownHosts:  knownHosts,
		auth:        auth,
	}
	if err := cli.initConfig(dnsConf); err != nil {
		cli.cleanup()
		return nil, nil, err
	}
	return cli, cli.cleanup, nil
}

func (cli *gitCLI) cleanup() {
	if cli.hostsPath != "" {
		os.Remove(cli.hostsPath)
	}
	if cli.resolvPath != "" {
		os.Remove(cli.resolvPath)
	}
}

func (cli *gitCLI) withinDir(gitDir, workDir string) *gitCLI {
	cp := *cli
	cp.gitDir = gitDir
	cp.workTree = workDir
	return &cp
}

func (cli *gitCLI) run(ctx context.Context, args ...string) (_ *bytes.Buffer, err error) {
	for {
		stdout, stderr, flush := logs.NewLogStreams(ctx, true)
		defer stdout.Close()
		defer stderr.Close()
		defer func() {
			if err != nil {
				flush()
			}
		}()
		if len(cli.auth) > 0 {
			args = append(cli.auth, args...)
		}
		if cli.gitDir != "" {
			args = append([]string{"--git-dir", cli.gitDir}, args...)
		}
		if cli.workTree != "" {
			args = append([]string{"--work-tree", cli.workTree}, args...)
		}
		// Block sneaky repositories from using repos from the filesystem as submodules.
		args = append([]string{"-c", "protocol.file.allow=user"}, args...)
		cmd := exec.Command("git", args...)
		cmd.Dir = cli.workTree // some commands like submodule require this
		buf := bytes.NewBuffer(nil)
		errbuf := bytes.NewBuffer(nil)
		cmd.Stdin = nil
		cmd.Stdout = io.MultiWriter(stdout, buf)
		cmd.Stderr = io.MultiWriter(stderr, errbuf)
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_SSH_COMMAND=" + getGitSSHCommand(cli.knownHosts),
			//	"GIT_TRACE=1",
			"GIT_CONFIG_NOSYSTEM=1", // Disable reading from system gitconfig.
			"HOME=/dev/null",        // Disable reading from user gitconfig.
			"LC_ALL=C",              // Ensure consistent output.
		}
		if cli.sshAuthSock != "" {
			cmd.Env = append(cmd.Env, "SSH_AUTH_SOCK="+cli.sshAuthSock)
		}
		// remote git commands spawn helper processes that inherit FDs and don't
		// handle parent death signal so exec.CommandContext can't be used
		err := runWithStandardUmaskAndNetOverride(ctx, cmd, cli.hostsPath, cli.resolvPath)
		if err != nil {
			if strings.Contains(errbuf.String(), "--depth") || strings.Contains(errbuf.String(), "shallow") {
				if newArgs := argsNoDepth(args); len(args) > len(newArgs) {
					args = newArgs
					continue
				}
			}
			return buf, errors.Errorf("git error: %s\nstderr:\n%s", err, errbuf.String())
		}
		return buf, nil
	}
}

func getGitSSHCommand(knownHosts string) string {
	gitSSHCommand := "ssh -F /dev/null"
	if knownHosts != "" {
		gitSSHCommand += " -o UserKnownHostsFile=" + knownHosts
	} else {
		gitSSHCommand += " -o StrictHostKeyChecking=no"
	}
	return gitSSHCommand
}

func argsNoDepth(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != "--depth=1" {
			out = append(out, a)
		}
	}
	return out
}
