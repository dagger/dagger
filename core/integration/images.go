package core

import "github.com/dagger/dagger/engine/distconsts"

const (
	alpineImage  = distconsts.AlpineImage
	wolfiImage   = "cgr.dev/chainguard/wolfi-base"
	busyboxImage = distconsts.BusyboxImage
	golangImage  = distconsts.GolangImage
	debianImage  = "debian:bookworm"
	rhelImage    = "registry.access.redhat.com/ubi9/ubi"
	alpineArm    = "arm64v8/alpine"
	alpineAmd    = "amd64/alpine"

	nodeImage   = "node:22.11.0-alpine@sha256:b64ced2e7cd0a4816699fe308ce6e8a08ccba463c757c00c14cd372e3d2c763e"
	pythonImage = "python:3.13-slim@sha256:4c2cf9917bd1cbacc5e9b07320025bdb7cdf2df7b0ceaccb55e9dd7e30987419"

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
