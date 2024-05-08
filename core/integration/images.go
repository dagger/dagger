package core

import "github.com/dagger/dagger/engine/distconsts"

const (
	alpineImage = distconsts.AlpineImage
	golangImage = distconsts.GolangImage
	debianImage = "debian:bookworm"
	rhelImage   = "registry.access.redhat.com/ubi9/ubi"
	alpineArm   = "arm64v8/alpine"

	// TODO: use these
	// registryImage   = "registry:2"
	// busyboxImage    = "busybox:1.36.0-musl"
	// pythonImage     = "python:3.11.2-slim"
	// nginxImage      = "nginx:1.23.3"
	// dockerDindImage = "docker:23.0.1-dind"
	// dockerCLIImage  = "docker:23.0.1-cli"
	// goxxImage       = "crazymax/goxx:1.19"
	// nanoserverImage = "mcr.microsoft.com/windows/nanoserver:ltsc2022"
)
