module dagger.cloud/go

go 1.13

require (
	cuelang.org/go v0.3.0-alpha6
	github.com/KromDaniel/jonson v0.0.0-20180630143114-d2f9c3c389db
	github.com/containerd/console v1.0.1
	github.com/emicklei/proto v1.9.0 // indirect
	github.com/moby/buildkit v0.8.1
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.0.0
	github.com/tonistiigi/fsutil v0.0.0-20201103201449-0834f99b7b85
	golang.org/x/sync v0.0.0-20200625203802-6e8e738ad208
	gopkg.in/yaml.v3 v3.0.0-20200506231410-2ff61e1afc86 // indirect
)

replace github.com/hashicorp/go-immutable-radix => github.com/tonistiigi/go-immutable-radix v0.0.0-20170803185627-826af9ccf0fe

replace github.com/jaguilar/vt100 => github.com/tonistiigi/vt100 v0.0.0-20190402012908-ad4c4a574305

replace github.com/containerd/containerd => github.com/containerd/containerd v1.3.1-0.20200227195959-4d242818bf55

replace github.com/docker/docker => github.com/docker/docker v1.4.2-0.20200227233006-38f52c9fec82
