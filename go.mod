module dagger.io/go

go 1.16

require (
	cuelang.org/go v0.4.0-alpha.2
	github.com/HdrHistogram/hdrhistogram-go v1.1.0 // indirect
	github.com/KromDaniel/jonson v0.0.0-20180630143114-d2f9c3c389db
	github.com/containerd/console v1.0.2
	github.com/docker/distribution v2.7.1+incompatible
	github.com/emicklei/proto v1.9.0 // indirect
	github.com/google/uuid v1.2.0
	github.com/jaguilar/vt100 v0.0.0-20150826170717-2703a27b14ea
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db
	github.com/moby/buildkit v0.8.3
	github.com/morikuni/aec v1.0.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opentracing/opentracing-go v1.2.0
	github.com/rs/zerolog v1.21.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/viper v1.7.1
	github.com/stretchr/testify v1.7.0
	github.com/tonistiigi/fsutil v0.0.0-20201103201449-0834f99b7b85
	github.com/tonistiigi/units v0.0.0-20180711220420-6950e57a87ea
	github.com/uber/jaeger-client-go v2.28.0+incompatible
	github.com/uber/jaeger-lib v2.4.1+incompatible // indirect
	go.mozilla.org/sops/v3 v3.7.1
	go.uber.org/atomic v1.7.0 // indirect
	golang.org/x/sync v0.0.0-20201020160332-67f06af15bc9
	golang.org/x/term v0.0.0-20201117132131-f5c789dd3221
	golang.org/x/time v0.0.0-20200416051211-89c76fbcd5d1
	google.golang.org/grpc v1.37.0
	gopkg.in/yaml.v3 v3.0.0-20210107172259-749611fa9fcc
)

replace (
	github.com/hashicorp/go-immutable-radix => github.com/tonistiigi/go-immutable-radix v0.0.0-20170803185627-826af9ccf0fe
	github.com/jaguilar/vt100 => github.com/tonistiigi/vt100 v0.0.0-20190402012908-ad4c4a574305
	// genproto: corresponds to containerd
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20200224152610-e50cd9704f63
)
