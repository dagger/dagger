package main

import (
	"fmt"
)

type NamespaceRunner struct {
	cores         int
	memory        int
	daggerVersion string
	ubuntuVersion string
	labels        []string
	withCaching   bool
}

func NewNamespaceRunner(
	daggerVersion string,
) NamespaceRunner {
	return NamespaceRunner{
		daggerVersion: daggerVersion,
		ubuntuVersion: "22.04",
		cores:         smallRunner,
		memory:        smallRunner * 2,
		withCaching:   false,
	}
}

func (r NamespaceRunner) RunsOn() []string {
	// We add size last in case the runner was customised
	return r.AddLabel(r.Size()).Labels()
}

func (r NamespaceRunner) AddLabel(label string) Runner {
	r.labels = append(r.labels, label)

	return r
}

func (r NamespaceRunner) Labels() []string {
	return r.labels
}

func (r NamespaceRunner) Size() string {
	var cached string
	if r.withCaching {
		cached = "-with-cache"
	}

	return fmt.Sprintf(
		"nscloud-ubuntu-%s-amd64-%dx%d%s",
		r.ubuntuVersion,
		r.cores,
		r.memory,
		cached)
}

func (r NamespaceRunner) Pipeline(name string) string {
	return fmt.Sprintf("%s-on-namespace", name)
}

func (r NamespaceRunner) Small() Runner {
	r.cores = smallRunner
	r.memory = smallRunner * 2
	return r
}

func (r NamespaceRunner) Medium() Runner {
	r.cores = mediumRunner
	r.memory = mediumRunner * 2
	return r
}

func (r NamespaceRunner) Large() Runner {
	r.cores = largeRunner
	r.memory = largeRunner * 2
	return r
}

func (r NamespaceRunner) XLarge() Runner {
	r.cores = xlargeRunner
	r.memory = xlargeRunner * 2
	return r
}

func (r NamespaceRunner) XXLarge() Runner {
	r.cores = xxlargeRunner
	r.memory = xxlargeRunner * 2
	return r
}

func (r NamespaceRunner) XXXLarge() Runner {
	// Infrastructure constraint - high-frequency cores
	r.cores = xxlargeRunner
	r.memory = xxlargeRunner * 2
	return r
}

func (r NamespaceRunner) SingleTenant() Runner {
	return r
}

func (r NamespaceRunner) DaggerInDocker() Runner {
	return r
}

func (r NamespaceRunner) Cached() Runner {
	r.withCaching = true
	return r
}
