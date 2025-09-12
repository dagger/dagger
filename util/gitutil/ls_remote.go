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
		return digest.FromString(remote.digest.String() + remote.Head.SHA + remote.Head.Name)
	}
	return remote.digest
}

func (remote *Remote) Tags() []*Ref {
	var tags []*Ref
	for _, ref := range remote.Refs {
		if strings.HasPrefix(ref.Name, "refs/tags/") {
			tags = append(tags, ref)
		}
	}
	return tags
}

func (remote *Remote) Branches() []*Ref {
	var branches []*Ref
	for _, ref := range remote.Refs {
		if strings.HasPrefix(ref.Name, "refs/heads/") {
			branches = append(branches, ref)
		}
	}
	return branches
}

func (remote *Remote) Lookup(target string) (*Ref, error) {
	if target == "HEAD" && remote.Head != nil {
		return remote.Head, nil
	}
	if IsCommitSHA(target) {
		return &Ref{Name: target, SHA: target}, nil
	}

	// simulate git-checkout semantics, and make sure to select exactly the right ref
	var (
		partialRef      = "refs/" + strings.TrimPrefix(target, "refs/")
		headRef         = "refs/heads/" + strings.TrimPrefix(target, "refs/heads/")
		tagRef          = "refs/tags/" + strings.TrimPrefix(target, "refs/tags/")
		annotatedTagRef = tagRef + "^{}"
	)
	var match, headMatch, tagMatch *Ref

	for _, ref := range remote.Refs {
		switch ref.Name {
		case headRef:
			headMatch = ref
		case tagRef, annotatedTagRef:
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
