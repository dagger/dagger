module go.dagger.io/dagger

go 1.16

require (
	cuelang.org/go v0.4.1-rc.1.0.20220106143633-60d6503d1974
	github.com/KromDaniel/jonson v0.0.0-20180630143114-d2f9c3c389db
	github.com/Microsoft/go-winio v0.5.2
	github.com/containerd/console v1.0.3
	github.com/containerd/containerd v1.6.3-0.20220401172941-5ff8fce1fcc6
	github.com/cyphar/filepath-securejoin v0.2.3 // indirect
	github.com/docker/buildx v0.8.2
	github.com/docker/distribution v2.8.1+incompatible
	github.com/emicklei/proto v1.9.0 // indirect
	github.com/go-git/go-git/v5 v5.4.2
	github.com/gofrs/flock v0.8.1
	github.com/google/uuid v1.3.0
	github.com/hashicorp/go-version v1.4.0
	github.com/lib/pq v1.10.0 // indirect
	github.com/mattn/go-colorable v0.1.12
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db
	github.com/mitchellh/go-homedir v1.1.0
	github.com/moby/buildkit v0.10.1
	github.com/morikuni/aec v1.0.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.3-0.20211202183452-c5a74bcca799
	github.com/rs/zerolog v1.26.1
	github.com/sergi/go-diff v1.2.0
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.4.0
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.11.0
	github.com/stretchr/testify v1.7.1
	github.com/tonistiigi/fsutil v0.0.0-20220315205639-9ed612626da3
	github.com/tonistiigi/units v0.0.0-20180711220420-6950e57a87ea
	github.com/tonistiigi/vt100 v0.0.0-20210615222946-8066bb97264f
	go.opentelemetry.io/otel v1.6.3
	go.opentelemetry.io/otel/exporters/jaeger v1.6.3
	go.opentelemetry.io/otel/sdk v1.6.3
	go.opentelemetry.io/otel/trace v1.6.3
	golang.org/x/mod v0.6.0-dev.0.20211013180041-c96bc1413d57
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/term v0.0.0-20210927222741-03fcf44c2211
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
	golang.org/x/tools v0.1.8 // indirect
	google.golang.org/grpc v1.45.0
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b
)

replace (
	cuelang.org/go => github.com/dagger/cue v0.4.1-rc.1.0.20220121023213-66df011a52c2
	github.com/docker/docker => github.com/docker/docker v20.10.3-0.20220121014307-40bb9831756f+incompatible
)
