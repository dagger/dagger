package gitutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

type Remote struct {
	Refs    []*Ref
	Symrefs map[string]string

	// override what HEAD points to, if set
	Head *Ref
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
	return hashutil.HashStrings(r.Name, r.SHA)
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
	}, nil
}

func (remote *Remote) Digest() digest.Digest {
	inputs := []string{}
	for _, ref := range remote.Refs {
		inputs = append(inputs, "ref", ref.Digest().String(), "\x00")
	}
	if remote.Head != nil {
		inputs = append(inputs, "head", remote.Head.Digest().String(), "\x00")
	}
	return hashutil.HashStrings(inputs...)
}

func (remote *Remote) withRefs(refs []*Ref) *Remote {
	return &Remote{
		Refs:    refs,
		Symrefs: remote.Symrefs,
		Head:    remote.Head,
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

func (remote *Remote) Get(name string) (result *Ref) {
	for _, ref := range remote.Refs {
		if ref.Name == name {
			return ref
		}
	}
	return nil
}

// Lookup looks up a ref by name, simulating git-checkout semantics.
// It handles full refs, partial refs, commits, symrefs, HEAD resolution, etc.
func (remote *Remote) Lookup(target string) (result *Ref, _ error) {
	isHead := target == "HEAD"
	if isHead && remote.Head != nil && remote.Head.Name != "" {
		// resolve HEAD to a specific ref
		target = remote.Head.Name
	}

	if IsCommitSHA(target) {
		return &Ref{SHA: target}, nil
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

	// clone the match to avoid weirdly mutating later
	clone := *match
	match = &clone

	// resolve symrefs to get the right ref result
	if ref, ok := remote.Symrefs[match.Name]; ok {
		match.Name = ref
	}

	if isHead && remote.Head != nil && remote.Head.SHA != "" {
		match.SHA = remote.Head.SHA
	}

	return match, nil
}
