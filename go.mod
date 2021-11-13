module go.dagger.io/dagger

go 1.16

require (
	cuelang.org/go v0.4.0
	filippo.io/age v1.0.0
	github.com/KromDaniel/jonson v0.0.0-20180630143114-d2f9c3c389db
	github.com/Microsoft/go-winio v0.5.1
	github.com/containerd/console v1.0.3
	github.com/containerd/containerd v1.5.7
	github.com/docker/buildx v0.7.0
	github.com/docker/distribution v2.7.1+incompatible
	github.com/emicklei/proto v1.9.0 // indirect
	github.com/go-git/go-git/v5 v5.4.2
	github.com/gofrs/flock v0.8.1
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/google/uuid v1.3.0
	github.com/hashicorp/go-version v1.3.0
	github.com/lib/pq v1.10.0 // indirect
	github.com/mattn/go-colorable v0.1.8 // indirect
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db
	github.com/mitchellh/go-homedir v1.1.0
	github.com/moby/buildkit v0.9.2
	github.com/morikuni/aec v1.0.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.2-0.20210819154149-5ad6f50d6283
	github.com/rs/zerolog v1.26.0
	github.com/spf13/cobra v1.2.1
	github.com/spf13/viper v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/tonistiigi/fsutil v0.0.0-20210818161904-4442383b5028
	github.com/tonistiigi/units v0.0.0-20180711220420-6950e57a87ea
	github.com/tonistiigi/vt100 v0.0.0-20210615222946-8066bb97264f
	go.mozilla.org/sops/v3 v3.7.1
	go.opentelemetry.io/otel v1.1.0
	go.opentelemetry.io/otel/exporters/jaeger v1.1.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.0.1 // indirect
	go.opentelemetry.io/otel/sdk v1.1.0
	go.opentelemetry.io/otel/trace v1.1.0
	golang.org/x/lint v0.0.0-20210508222113-6edffad5e616 // indirect
	golang.org/x/mod v0.5.1
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/sys v0.0.0-20211004093028-2c5d950f24ef // indirect
	golang.org/x/term v0.0.0-20210615171337-6886f2dfbf5b
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
	google.golang.org/grpc v1.42.0
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b
)

replace (
	github.com/hashicorp/go-immutable-radix => github.com/tonistiigi/go-immutable-radix v0.0.0-20170803185627-826af9ccf0fe
	// genproto: corresponds to containerd
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20200224152610-e50cd9704f63
)
