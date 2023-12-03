package core

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/moby/buildkit/client/llb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type GitRef struct {
	URL string `json:"url"`
	Ref string `json:"ref"`

	KeepGitDir bool `json:"keepGitDir"`

	SSHKnownHosts string    `json:"sshKnownHosts"`
	SSHAuthSocket socket.ID `json:"sshAuthSocket"`

	Services ServiceBindings `json:"services"`
	Pipeline pipeline.Path   `json:"pipeline"`
	Platform specs.Platform  `json:"platform,omitempty"`
}

func (ref *GitRef) clone() *GitRef {
	r := *ref
	r.Services = cloneSlice(r.Services)
	r.Pipeline = cloneSlice(r.Pipeline)
	return &r
}

func (ref *GitRef) WithRef(name string) *GitRef {
	ref = ref.clone()
	ref.Ref = name
	return ref
}

func (ref *GitRef) DefaultBranch(ctx context.Context, bk *buildkit.Client) (string, error) {
	output, err := exec.CommandContext(ctx, "git", "ls-remote", "--symref", ref.URL, "HEAD").Output() // nolint:gosec
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(bytes.NewBuffer(output))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		if fields[0] == "ref:" && fields[2] == "HEAD" {
			return strings.TrimPrefix(fields[1], "refs/heads/"), nil
		}
	}

	return "", fmt.Errorf("could not parse default branch from output:\n%s", output)
}

func (ref *GitRef) Tags(ctx context.Context, bk *buildkit.Client, patterns ...string) ([]string, error) {
	args := []string{
		"ls-remote",
		"--tags", // we only want tags
		"--refs", // we don't want to include ^{} entries for annotated tags
		ref.URL,
	}
	args = append(args, patterns...)

	output, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewBuffer(output))

	tags := []string{}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		// this API is to fetch tags, not refs, so we can drop the `refs/tags/`
		// prefix
		tag := strings.TrimPrefix(fields[1], "refs/tags/")

		tags = append(tags, tag)
	}

	return tags, nil
}

func (ref *GitRef) Tree(ctx context.Context, bk *buildkit.Client) (*Directory, error) {
	st := ref.getState(ctx, bk)
	return NewDirectorySt(ctx, *st, "", ref.Pipeline, ref.Platform, ref.Services)
}

func (ref *GitRef) Commit(ctx context.Context, bk *buildkit.Client) (string, error) {
	st := ref.getState(ctx, bk)
	p, err := resolveProvenance(ctx, bk, *st)
	if err != nil {
		return "", err
	}
	if len(p.Sources.Git) == 0 {
		return "", errors.Errorf("no git commit was resolved")
	}
	return p.Sources.Git[0].Commit, nil
}

func (ref *GitRef) getState(ctx context.Context, bk *buildkit.Client) *llb.State {
	opts := []llb.GitOption{}

	if ref.KeepGitDir {
		opts = append(opts, llb.KeepGitDir())
	}
	if ref.SSHKnownHosts != "" {
		opts = append(opts, llb.KnownSSHHosts(ref.SSHKnownHosts))
	}
	if ref.SSHAuthSocket != "" {
		opts = append(opts, llb.MountSSHSock(string(ref.SSHAuthSocket)))
	}

	useDNS := len(ref.Services) > 0

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err == nil && !useDNS {
		useDNS = len(clientMetadata.ParentClientIDs) > 0
	}

	var st llb.State
	if useDNS {
		// NB: only configure search domains if we're directly using a service, or
		// if we're nested beneath another search domain.
		//
		// we have to be a bit selective here to avoid breaking Dockerfile builds
		// that use a Buildkit frontend (# syntax = ...) that doesn't have the
		// networks API cap.
		//
		// TODO: add API cap
		st = gitdns.State(ref.URL, ref.Ref, clientMetadata.ClientIDs(), opts...)
	} else {
		st = llb.Git(ref.URL, ref.Ref, opts...)
	}
	return &st
}
