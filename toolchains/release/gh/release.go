package main

import (
	"context"
	"dagger/gh/internal/dagger"
	"path"
)

// Manage releases.
func (m *Gh) Release() *Release {
	return &Release{Gh: m}
}

type Release struct {
	// +private
	Gh *Gh
}

type Latest string

const (
	LatestTrue  Latest = "LATEST_TRUE"
	LatestFalse Latest = "LATEST_FALSE"
	LatestAuto  Latest = "LATEST_AUTO"
)

// Create a new GitHub Release for a repository.
func (m *Release) Create(
	ctx context.Context,

	// Tag this release should point to or create.
	tag string,

	// Release title.
	title string,

	// Release assets to upload.
	//
	// +optional
	files []*dagger.File,

	// Save the release as a draft instead of publishing it.
	//
	// +optional
	draft bool,

	// Mark the release as a prerelease.
	//
	// +optional
	preRelease bool,

	// Target branch or full commit SHA (default: main branch).
	//
	// +optional
	target string,

	// Release notes.
	//
	// +optional
	notes string,

	// Read release notes from file.
	//
	// +optional
	notesFile *dagger.File,

	// Start a discussion in the specified category.
	//
	// +optional
	discussionCategory string,

	// Automatically generate title and notes for the release.
	//
	// +optional
	generateNotes bool,

	// Tag to use as the starting point for generating release notes.
	//
	// +optional
	notesStartTag string,

	// Mark this release as "Latest" (default: automatic based on date and version).
	//
	// +optional
	// +default="LATEST_AUTO"
	latest Latest,

	// Abort in case the git tag doesn't already exist in the remote repository.
	//
	// +optional
	verifyTag bool,

	// Tag to use as the starting point for generating release notes.
	//
	// +optional
	notesFromTag bool,

	// GitHub token.
	//
	// +optional
	token *dagger.Secret,

	// GitHub repository (e.g. "owner/repo").
	//
	// +optional
	repo string,
) error {
	ctr := m.Gh.container(token, repo)

	args := []string{
		"gh", "release", "create",

		"--title", title,
	}

	if draft {
		args = append(args, "--draft")
	}

	if preRelease {
		args = append(args, "--prerelease")
	}

	if target != "" {
		args = append(args, "--target", target)
	}

	if notes != "" {
		args = append(args, "--notes", notes)
	}

	if notesFile != nil {
		ctr = ctr.WithMountedFile("/work/tmp/notes.md", notesFile)
		args = append(args, "--notes-file", "/work/tmp/notes.md")
	}

	if discussionCategory != "" {
		args = append(args, "--discussion-category", discussionCategory)
	}

	if generateNotes {
		args = append(args, "--generate-notes")
	}

	if notesStartTag != "" {
		args = append(args, "--notes-start-tag", notesStartTag)
	}

	switch latest {
	case LatestAuto:
		// automatic based on date and version
	case LatestTrue:
		args = append(args, "--latest=true")
	case LatestFalse:
		args = append(args, "--latest=false")
	}

	if verifyTag {
		args = append(args, "--verify-tag")
	}

	if notesFromTag {
		args = append(args, "--notes-from-tag")
	}

	args = append(args, tag)

	{
		// TODO: use WithFiles
		dir := dag.Directory()

		for _, file := range files {
			dir = dir.WithFile("", file)
		}

		entries, err := dir.Entries(ctx)
		if err != nil {
			return err
		}

		ctr = ctr.WithMountedDirectory("/work/assets", dir)

		for _, e := range entries {
			args = append(args, path.Join("/work/assets", e))
		}
	}

	_, err := ctr.WithExec(args).Sync(ctx)

	return err
}
