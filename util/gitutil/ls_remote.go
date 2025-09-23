package gitutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencontainers/go-digest"
)

type Remote struct {
	Refs    []*Ref
	Symrefs map[string]string

	// override what HEAD points to, if set
	Head *Ref

	digest digest.Digest
}

type Ref struct {
	// Name is the fully resolved ref name, e.g. refs/heads/main or refs/tags/v1.0.0 or a commit SHA
	Name string

	// SHA is the commit SHA the ref points to
	SHA string
}

func (r *Ref) ShortName() string {
	if IsCommitSHA(r.Name) {
		return r.Name
	}
	if name, ok := strings.CutPrefix(r.Name, "refs/heads/"); ok {
		return name
	}
	if name, ok := strings.CutPrefix(r.Name, "refs/tags/"); ok {
		return name
	}
	if name, ok := strings.CutPrefix(r.Name, "refs/remotes/"); ok {
		return name
	}
	if name, ok := strings.CutPrefix(r.Name, "refs/"); ok {
		return name
	}
	return r.Name
}

func (r *Ref) Digest() digest.Digest {
	return digest.FromString(strings.Join([]string{
		r.Name,
		r.SHA,
	}, "\x00"))
}

func (cli *GitCLI) LsRemote(ctx context.Context, remote string) (*Remote, error) {
	out, err := cli.Run(ctx,
		"ls-remote",
		"--symref",
		remote,
	)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")

	refs := make([]*Ref, 0, len(lines))
	symrefs := make(map[string]string)

	for _, line := range lines {
		k, v, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}

		if target, ok := strings.CutPrefix(k, "ref: "); ok {
			// this is a symref, record it for later
			symrefs[v] = target
		} else {
			// normal ref
			refs = append(refs, &Ref{SHA: k, Name: v})
		}
	}

	return &Remote{
		Refs:    refs,
		Symrefs: symrefs,
		digest:  digest.FromBytes(out),
	}, nil
}

func (remote *Remote) Digest() digest.Digest {
	if remote.Head != nil {
		return digest.FromString(strings.Join([]string{
			remote.digest.String(),
			remote.Head.SHA,
			remote.Head.Name,
		}, "\x00"))
	}
	return remote.digest
}

func (remote *Remote) withRefs(refs []*Ref) *Remote {
	return &Remote{
		Refs:    refs,
		Symrefs: remote.Symrefs,
		Head:    remote.Head,
		digest:  remote.digest,
	}
}

func (remote *Remote) Tags() *Remote {
	var tags []*Ref
	for _, ref := range remote.Refs {
		if !strings.HasPrefix(ref.Name, "refs/tags/") {
			continue // skip non-tags
		}
		if strings.HasSuffix(ref.Name, "^{}") {
			continue // skip unpeeled tags, we'll include the peeled version instead
		}
		tags = append(tags, ref)
	}
	return remote.withRefs(tags)
}

func (remote *Remote) Branches() *Remote {
	var branches []*Ref
	for _, ref := range remote.Refs {
		if !strings.HasPrefix(ref.Name, "refs/heads/") {
			continue // skip non-branches
		}
		branches = append(branches, ref)
	}
	return remote.withRefs(branches)
}

func (remote *Remote) Filter(patterns []string) *Remote {
	if len(patterns) == 0 {
		return remote
	}
	var refs []*Ref
	for _, ref := range remote.Refs {
		matched := false
		for _, pattern := range patterns {
			ok, _ := gitTailMatch(pattern, ref.Name)
			if ok {
				matched = true
				break
			}
		}
		if matched {
			refs = append(refs, ref)
		}
	}
	return remote.withRefs(refs)
}

func (remote *Remote) ShortNames() []string {
	names := make([]string, len(remote.Refs))
	for i, ref := range remote.Refs {
		names[i] = ref.ShortName()
	}
	return names
}

func (remote *Remote) Lookup(target string) (*Ref, error) {
	if target == "HEAD" && remote.Head != nil {
		if remote.Head.SHA != "" {
			// head is already fully resolved
			return remote.Head, nil
		}
		// head isn't resolved, so just use what it points to
		target = remote.Head.Name
	}
	if IsCommitSHA(target) {
		return &Ref{Name: target, SHA: target}, nil
	}

	// simulate git-checkout semantics, and make sure to select exactly the right ref
	var (
		partialRef   = "refs/" + strings.TrimPrefix(target, "refs/")
		headRef      = "refs/heads/" + strings.TrimPrefix(target, "refs/heads/")
		tagRef       = "refs/tags/" + strings.TrimPrefix(target, "refs/tags/")
		peeledTagRef = tagRef + "^{}"
	)
	var match, headMatch, tagMatch *Ref

	for _, ref := range remote.Refs {
		switch ref.Name {
		case headRef:
			headMatch = ref
		case tagRef, peeledTagRef:
			tagMatch = ref
			tagMatch.Name = tagRef
		case partialRef:
			match = ref
		case target:
			match = ref
		}
	}
	// git-checkout prefers branches in case of ambiguity
	if match == nil {
		match = headMatch
	}
	if match == nil {
		match = tagMatch
	}
	if match == nil {
		return nil, fmt.Errorf("repository does not contain ref %q", target)
	}
	if !IsCommitSHA(match.SHA) {
		return nil, fmt.Errorf("invalid commit sha %q for %q", match.SHA, match.Name)
	}

	// resolve symrefs to get the right ref result
	if ref, ok := remote.Symrefs[match.Name]; ok {
		clone := *match
		match = &clone
		match.Name = ref
	}

	return match, nil
}
