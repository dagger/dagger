package dagger

// A ref is an address for a remote container image
//
// Examples:
//   - "index.docker.io/dagger"
//   - "dagger"
//   - "index.docker.io/dagger:latest"
//   - "index.docker.io/dagger:latest@sha256:a89cb097693dd354de598d279c304a1c73ee550fbfff6d9ee515568e0c749cfe"
#Ref: string

// Container image config. See [OCI](https://www.opencontainers.org/).
#ImageConfig: {
	user?: string
	expose?: [string]: {}
	env?: [string]: string
	entrypoint?: [...string]
	cmd?: [...string]
	volume?: [string]: {}
	workdir?: string
	label?: [string]: string
	stopsignal?:  string
	healthcheck?: #HealthCheck
	argsescaped?: bool
	onbuild?: [...string]
	stoptimeout?: int
	shell?: [...string]
}

#HealthCheck: {
	test?: [...string]
	interval?:    int
	timeout?:     int
	startperiod?: int
	retries?:     int
}
