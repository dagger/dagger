package main

import (
	"fmt"
	"strings"
)

type DepotRunner struct {
	cores         int
	daggerVersion string
	labels        []string
	ubuntuVersion string
	withCaching   bool
}

func NewDepotRunner(
	daggerVersion string,
) DepotRunner {
	return DepotRunner{
		daggerVersion: daggerVersion,
		ubuntuVersion: "22.04",
		withCaching:   false,
	}
}

func (r DepotRunner) RunsOn() []string {
	// We add size last in case the runner was customised
	return r.AddLabel(r.Size()).Labels()
}

func (r DepotRunner) AddLabel(label string) Runner {
	r.labels = append(r.labels, label)

	return r
}

func (r DepotRunner) Labels() []string {
	return r.labels
}

func (r DepotRunner) Size() string {
	// Enabling caching in this context implies pre-provisioned Dagger
	// And no size for the CI runner itself (Dagger Engine is the right size)
	if r.withCaching {
		return fmt.Sprintf(
			"depot-ubuntu-%s,dagger=%s",
			r.ubuntuVersion,
			strings.ReplaceAll(r.daggerVersion, "v", ""))
	}

	return fmt.Sprintf(
		"depot-ubuntu-%s-%d",
		r.ubuntuVersion,
		r.cores)
}

func (r DepotRunner) Pipeline(name string) string {
	return fmt.Sprintf("%s-on-depot", name)
}

func (r DepotRunner) Small() Runner {
	r.cores = smallRunner
	return r
}

func (r DepotRunner) Medium() Runner {
	r.cores = mediumRunner
	return r
}

func (r DepotRunner) Large() Runner {
	r.cores = largeRunner
	return r
}

func (r DepotRunner) XLarge() Runner {
	r.cores = xlargeRunner
	return r
}

func (r DepotRunner) XXLarge() Runner {
	r.cores = xxlargeRunner
	return r
}

func (r DepotRunner) XXXLarge() Runner {
	r.cores = xxxlargeRunner
	return r
}

func (r DepotRunner) SingleTenant() Runner {
	return r
}

func (r DepotRunner) DaggerInDocker() Runner {
	return r
}

func (r DepotRunner) Cached() Runner {
	r.withCaching = true
	return r
}
