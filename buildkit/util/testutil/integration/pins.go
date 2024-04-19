package integration

var pins = map[string]map[string]string{
	// busybox 1.36
	"busybox:latest": {
		"amd64":   "sha256:023917ec6a886d0e8e15f28fb543515a5fcd8d938edb091e8147db4efed388ee",
		"arm64v8": "sha256:1fa89c01cd0473cedbd1a470abb8c139eeb80920edf1bc55de87851bfb63ea11",
		"library": "sha256:3fbc632167424a6d997e74f52b878d7cc478225cffac6bc977eedfe51c7f4e79",
	},
	// alpine 3.18
	"alpine:latest": {
		"amd64":   "sha256:25fad2a32ad1f6f510e528448ae1ec69a28ef81916a004d3629874104f8a7f70",
		"arm64v8": "sha256:e3bd82196e98898cae9fe7fbfd6e2436530485974dc4fb3b7ddb69134eda2407",
		"library": "sha256:82d1e9d7ed48a7523bdebc18cf6290bdb97b82302a8a9c27d4fe885949ea94d1",
	},
}
