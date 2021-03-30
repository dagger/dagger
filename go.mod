module dagger.io/go

go 1.16

require (
	cuelang.org/go v0.3.0-beta.7
	github.com/KromDaniel/jonson v0.0.0-20180630143114-d2f9c3c389db
	github.com/containerd/console v1.0.1
	github.com/docker/distribution v2.7.1+incompatible
	github.com/emicklei/proto v1.9.0 // indirect
	github.com/google/uuid v1.2.0 // indirect
	github.com/jaguilar/vt100 v0.0.0-20150826170717-2703a27b14ea
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db
	github.com/moby/buildkit v0.8.2
	github.com/morikuni/aec v1.0.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opentracing/opentracing-go v1.2.0
	github.com/rs/zerolog v1.20.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.7.1
	github.com/stretchr/testify v1.5.1 // indirect
	github.com/tonistiigi/fsutil v0.0.0-20201103201449-0834f99b7b85
	github.com/tonistiigi/units v0.0.0-20180711220420-6950e57a87ea
	github.com/uber/jaeger-client-go v2.25.0+incompatible
	go.mozilla.org/sops v0.0.0-20190912205235-14a22d7a7060
	golang.org/x/sync v0.0.0-20201020160332-67f06af15bc9
	golang.org/x/sys v0.0.0-20210124154548-22da62e12c0c // indirect
	golang.org/x/term v0.0.0-20201117132131-f5c789dd3221
	golang.org/x/time v0.0.0-20200416051211-89c76fbcd5d1
	golang.org/x/tools v0.1.0 // indirect
	gopkg.in/yaml.v3 v3.0.0-20200506231410-2ff61e1afc86
)

replace (
	// protobuf: corresponds to containerd
	github.com/golang/protobuf => github.com/golang/protobuf v1.3.5
	github.com/hashicorp/go-immutable-radix => github.com/tonistiigi/go-immutable-radix v0.0.0-20170803185627-826af9ccf0fe
	github.com/jaguilar/vt100 => github.com/tonistiigi/vt100 v0.0.0-20190402012908-ad4c4a574305
	// genproto: corresponds to containerd
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20200224152610-e50cd9704f63

  cuelang.org/go => ../../cue/gerrit
)
