package main

import (
	"fmt"

	goapk "chainguard.dev/apko/pkg/apk/apk"
)

const (
	wolfiRepository      = "https://packages.wolfi.dev"
	chainguardRepository = "https://packages.cgr.dev"
)

func wolfiRepositories() []string {
	osRepo := fmt.Sprintf("%s/%s", wolfiRepository, "os")
	extrasRepo := fmt.Sprintf("%s/%s", chainguardRepository, "extras")
	return []string{osRepo, extrasRepo}
}

func wolfiReleases() *goapk.Releases {
	arches := []string{"aarch64", "x86_64"}

	wolfiRepo := "os"
	wolfiSigningKey := fmt.Sprintf("%s/%s/wolfi-signing.rsa.pub", wolfiRepository, wolfiRepo)

	extrasRepo := "extras"
	extrasSigningKey := fmt.Sprintf("%s/%s/chainguard-extras.rsa.pub", chainguardRepository, extrasRepo)

	return &goapk.Releases{
		Architectures: arches,
		LatestStable:  "main",
		ReleaseBranches: []goapk.ReleaseBranch{
			{
				ReleaseBranch: "main",
				GitBranch:     "main",
				Arches:        arches,
				Repos: []goapk.Repo{
					{Name: wolfiRepo},
					{Name: extrasRepo},
				},
				Keys: map[string][]goapk.RepoKeys{
					"aarch64": {
						{URL: wolfiSigningKey},
						{URL: extrasSigningKey},
					},
					"x86_64": {
						{URL: wolfiSigningKey},
						{URL: extrasSigningKey},
					},
				},
			},
		},
	}
}
