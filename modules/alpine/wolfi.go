package main

import (
	"fmt"

	goapk "chainguard.dev/apko/pkg/apk/apk"
)

const (
	wolfiRepository = "https://packages.wolfi.dev"
)

func wolfiRepositories() []string {
	osRepo := fmt.Sprintf("%s/%s", wolfiRepository, "os")
	return []string{osRepo}
}

func wolfiReleases() *goapk.Releases {
	arches := []string{"aarch64", "x86_64"}
	repo := "os"
	signingKey := fmt.Sprintf("%s/%s/wolfi-signing.rsa.pub", wolfiRepository, repo)

	return &goapk.Releases{
		Architectures: arches,
		LatestStable:  "main",
		ReleaseBranches: []goapk.ReleaseBranch{
			{
				ReleaseBranch: "main",
				GitBranch:     "main",
				Arches:        arches,
				Repos:         []goapk.Repo{{Name: repo}},
				Keys: map[string][]goapk.RepoKeys{
					"aarch64": {
						{
							URL: signingKey,
						},
					},
					"x86_64": {
						{
							URL: signingKey,
						},
					},
				},
			},
		},
	}
}
