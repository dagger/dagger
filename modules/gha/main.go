// Manage Github Actions configurations with Dagger
//
// Daggerizing your CI makes your YAML configurations smaller, but they still exist,
// and they're still a pain to maintain by hand.
//
// This module aims to finish the job, by letting you generate your remaining
// YAML configuration from a Dagger pipeline, written in your favorite language.
package main

import "github.com/dagger/dagger/modules/gha/internal/dagger"

type Gha struct {
	Workflows []*Workflow

	JobDefaults      *Job      // +private
	WorkflowDefaults *Workflow // +private
}

func New(
	jobDefaults *Job, // +optional
	workflowDefaults *Workflow, // +optional
) *Gha {
	return &Gha{
		JobDefaults:      jobDefaults,
		WorkflowDefaults: workflowDefaults,
	}
}

func (gha *Gha) Generate(
	// +optional
	directory *dagger.Directory,
	// +optional
	asJSON bool,
	// +optional
	// +default=".gen.yml"
	fileExtension string,
) *dagger.Directory {
	if directory == nil {
		directory = dag.Directory()
	}
	directory = directory.With(deleteOldFiles(fileExtension))
	for _, p := range gha.Workflows {
		directory = directory.WithDirectory(".", p.config(asJSON, fileExtension))
	}
	directory = directory.With(gitAttributes(fileExtension))
	return directory
}

func gitAttributes(fileExtension string) func(*dagger.Directory) *dagger.Directory {
	// Need a custom file extension to match generated files in .gitattributes
	if ext := fileExtension; ext == ".yml" || ext == ".yaml" {
		return nil
	}

	return func(d *dagger.Directory) *dagger.Directory {
		return d.WithNewFile(
			".github/workflows/.gitattributes",
			"*"+fileExtension+" linguist-generated")
	}
}

func deleteOldFiles(fileExtension string) func(*dagger.Directory) *dagger.Directory {
	// Need a custom file extension to delete old files
	if ext := fileExtension; ext == ".yml" || ext == ".yaml" {
		return nil
	}

	return func(d *dagger.Directory) *dagger.Directory {
		return dag.Directory().WithDirectory("", d, dagger.DirectoryWithDirectoryOpts{
			Exclude: []string{".github/workflows/*" + fileExtension},
		})
	}
}
