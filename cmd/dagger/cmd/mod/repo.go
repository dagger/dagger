package mod

import (
	"fmt"
	"os"
	"path"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/hashicorp/go-version"
)

type repo struct {
	localPath string
	contents  *git.Repository
}

func clone(require *require, dir string) (*repo, error) {
	r, err := git.PlainClone(dir, false, &git.CloneOptions{
		URL: require.cloneURL(),
	})
	if err != nil {
		return nil, err
	}

	rr := &repo{
		localPath: dir,
		contents:  r,
	}

	if require.version == "" {
		require.version, err = rr.latestTag()
		if err != nil {
			return nil, err
		}
	}

	if err := rr.checkout(require.version); err != nil {
		return nil, err
	}

	if _, err := os.Stat(path.Join(dir, require.path, FilePath)); err != nil {
		return nil, fmt.Errorf("repo does not contain %s", FilePath)
	}

	return rr, nil
}

func (r *repo) checkout(version string) error {
	h, err := r.contents.ResolveRevision(plumbing.Revision(version))
	if err != nil {
		return err
	}

	w, err := r.contents.Worktree()
	if err != nil {
		return err
	}

	err = w.Checkout(&git.CheckoutOptions{
		Hash: *h,
	})
	if err != nil {
		return err
	}

	return nil
}

func (r *repo) listTags() ([]string, error) {
	iter, err := r.contents.Tags()
	if err != nil {
		return nil, err
	}

	var tags []string
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		tags = append(tags, ref.Name().Short())
		return nil
	}); err != nil {
		return nil, err
	}

	return tags, nil
}

func (r *repo) latestTag() (string, error) {
	versionsRaw, err := r.listTags()
	if err != nil {
		return "", err
	}

	versions := make([]*version.Version, len(versionsRaw))
	for i, raw := range versionsRaw {
		v, _ := version.NewVersion(raw)
		versions[i] = v
	}

	// After this, the versions are properly sorted
	sort.Sort(version.Collection(versions))

	return versions[len(versions)-1].Original(), nil
}
