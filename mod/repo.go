package mod

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/rs/zerolog/log"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/hashicorp/go-version"
)

type repo struct {
	contents *git.Repository
	require  *Require
}

func clone(ctx context.Context, require *Require, dir string, privateKeyFile, privateKeyPassword string) (*repo, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("error cleaning up tmp directory")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("error creating tmp dir for cloning package")
	}

	o := git.CloneOptions{
		URL: fmt.Sprintf("https://%s", require.cloneRepo),
	}

	if privateKeyFile != "" {
		publicKeys, err := ssh.NewPublicKeysFromFile("git", privateKeyFile, privateKeyPassword)
		if err != nil {
			return nil, err
		}

		o.Auth = publicKeys
		o.URL = fmt.Sprintf("git@%s", strings.Replace(require.cloneRepo, "/", ":", 1))
	}

	r, err := git.PlainClone(dir, false, &o)
	if err != nil {
		return nil, err
	}

	rr := &repo{
		contents: r,
		require:  require,
	}

	if require.version == "" {
		latestTag, err := rr.latestTag(ctx, require.versionConstraint)
		if err != nil {
			return nil, err
		}

		require.version = latestTag
	}

	if err := rr.checkout(ctx, require.version); err != nil {
		return nil, err
	}

	return rr, nil
}

func (r *repo) checkout(ctx context.Context, version string) error {
	lg := log.Ctx(ctx)

	h, err := r.contents.ResolveRevision(plumbing.Revision(version))
	if err != nil {
		return err
	}

	lg.Debug().Str("repository", r.require.repo).Str("version", version).Str("commit", h.String()).Msg("checkout repo")

	w, err := r.contents.Worktree()
	if err != nil {
		return err
	}

	err = w.Checkout(&git.CheckoutOptions{
		Hash:  *h,
		Force: true,
	})
	if err != nil {
		return err
	}

	return nil
}

func (r *repo) listTagVersions(ctx context.Context, versionConstraint string) ([]string, error) {
	lg := log.Ctx(ctx).With().
		Str("repository", r.require.repo).
		Str("versionConstraint", versionConstraint).
		Logger()

	if versionConstraint == "" {
		versionConstraint = ">= 0"
	}

	constraint, err := version.NewConstraint(versionConstraint)
	if err != nil {
		return nil, err
	}

	iter, err := r.contents.Tags()
	if err != nil {
		return nil, err
	}

	var tags []string
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		tagV := ref.Name().Short()

		if !strings.HasPrefix(tagV, "v") {
			lg.Debug().Str("tag", tagV).Msg("tag version ignored, wrong format")
			return nil
		}

		v, err := version.NewVersion(tagV)
		if err != nil {
			lg.Debug().Str("tag", tagV).Err(err).Msg("tag version ignored, parsing error")
			return nil
		}

		if constraint.Check(v) {
			// Add tag if it matches the version constraint
			tags = append(tags, ref.Name().Short())
			lg.Debug().Str("tag", tagV).Msg("version added")
		} else {
			lg.Debug().Str("tag", tagV).Msg("tag version ignored, does not satisfy constraint")
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return tags, nil
}

func (r *repo) latestTag(ctx context.Context, versionConstraint string) (string, error) {
	versionsRaw, err := r.listTagVersions(ctx, versionConstraint)
	if err != nil {
		return "", err
	}

	versions := make([]*version.Version, len(versionsRaw))
	for i, raw := range versionsRaw {
		v, _ := version.NewVersion(raw)
		versions[i] = v
	}

	if len(versions) == 0 {
		return "", fmt.Errorf("repo doesn't have any tags matching a latest version (expected format: vX.Y.Z)")
	}

	sort.Sort(sort.Reverse(version.Collection(versions)))
	version := versions[0].Original()

	return version, nil
}
