package main

import (
	"fmt"
	"strings"
)

const (
	generation = 2

	smallRunner    = 2
	mediumRunner   = 4
	largeRunner    = 8
	xlargeRunner   = 16
	xxlargeRunner  = 32
	xxxlargeRunner = 64
)

type Runner interface {
	Small() Runner
	Medium() Runner
	Large() Runner
	XLarge() Runner
	XXLarge() Runner
	XXXLarge() Runner

	SingleTenant() Runner
	DaggerInDocker() Runner
	Cached() Runner

	Size() string
	AddLabel(string) Runner
	Labels() []string

	Pipeline(string) string
	RunsOn() []string
}

type DaggerRunner struct {
	cores          int
	daggerVersion  string
	daggerInDocker bool
	generation     int
	labels         []string
	singleTenant   bool
	withCaching    bool
}

func NewDaggerRunner(
	daggerVersion string,
) DaggerRunner {
	return DaggerRunner{
		generation:    generation,
		daggerVersion: daggerVersion,
		labels:        []string{},
		withCaching:   false,
	}
}

func (r DaggerRunner) RunsOn() []string {
	// We add size last in case the runner was customised
	return r.AddLabel(r.Size()).Labels()
}

func (r DaggerRunner) AddLabel(label string) Runner {
	r.labels = append(r.labels, label)

	return r
}

func (r DaggerRunner) Labels() []string {
	return r.labels
}

func (r DaggerRunner) Size() string {
	size := fmt.Sprintf("dagger-g%d-%s-%dc",
		r.generation,
		strings.ReplaceAll(r.daggerVersion, ".", "-"),
		r.cores)
	if r.daggerInDocker {
		size += "-dind"
	}
	if r.singleTenant {
		size += "-st"
	}

	return fmt.Sprintf(
		"${{ github.repository == '%s' && '%s' || '%s' }}",
		upstreamRepository,
		size,
		defaultRunner)
}

func (r DaggerRunner) Pipeline(name string) string {
	return name
}

func (r DaggerRunner) Small() Runner {
	r.cores = smallRunner
	return r
}

func (r DaggerRunner) Medium() Runner {
	r.cores = mediumRunner
	return r
}

func (r DaggerRunner) Large() Runner {
	r.cores = largeRunner
	return r
}

func (r DaggerRunner) XLarge() Runner {
	r.cores = xlargeRunner
	return r
}

func (r DaggerRunner) XXLarge() Runner {
	r.cores = xxlargeRunner
	return r
}

func (r DaggerRunner) XXXLarge() Runner {
	// Infrastructure constraint - EC2 instance sizing
	r.cores = xxlargeRunner
	return r
}

func (r DaggerRunner) SingleTenant() Runner {
	r.singleTenant = true
	return r
}

func (r DaggerRunner) DaggerInDocker() Runner {
	r.daggerInDocker = true
	return r
}

func (r DaggerRunner) Cached() Runner {
	// There is no option to enable caching in the current generation
	return r
}
