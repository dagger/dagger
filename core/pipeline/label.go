package pipeline

import (
	"sync"

	"github.com/go-git/go-git/v5"
)

var (
	loadOnce      sync.Once
	loadDoneCh    = make(chan struct{})
	defaultLabels []Label
)

type Label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RootLabels returns default labels for Pipelines.
//
// `LoadRootLabels` *must* be called before invoking this function.
// `RootLabels` will wait until `LoadRootLabels` has completed.
func RootLabels() []Label {
	<-loadDoneCh
	return defaultLabels
}

// LoadRootLabels loads default Pipeline labels from a workdir.
func LoadRootLabels(workdir string) error {
	var err error
	loadOnce.Do(func() {
		defer close(loadDoneCh)

		defaultLabels, err = loadRootLabels(workdir)
	})
	return err
}

func loadRootLabels(workdir string) ([]Label, error) {
	repo, err := git.PlainOpenWithOptions(workdir, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return nil, err
	}

	origin, err := repo.Remote("origin")
	if err != nil {
		return nil, err
	}

	urls := origin.Config().URLs
	if len(urls) == 0 {
		return []Label{}, nil
	}

	endpoint, err := parseGitURL(urls[0])
	if err != nil {
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, err
	}

	return []Label{
		{
			Name:  "dagger.io/git.remote",
			Value: endpoint,
		},
		{
			Name:  "dagger.io/git.branch",
			Value: head.Name().Short(),
		},
		{
			Name:  "dagger.io/git.ref",
			Value: head.Hash().String(),
		},
	}, nil
}
