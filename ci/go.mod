module github.com/dagger/dagger/ci

go 1.21.7

replace (
	dagger.io/dagger => ../sdk/go
	github.com/dagger/dagger => ../
)

require (
	dagger.io/dagger v0.10.3
	github.com/dagger/dagger v0.10.3
)

require (
	github.com/99designs/gqlgen v0.17.44
	github.com/Khan/genqlient v0.7.0
	github.com/containerd/containerd v1.7.15-0.20240329193453-0dcf21c1528a
	github.com/magefile/mage v1.15.0
	github.com/moby/buildkit v0.13.0-rc3.0.20240403135707-dc23e43dc15c
	github.com/opencontainers/image-spec v1.1.0
	github.com/vektah/gqlparser/v2 v2.5.11
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa
	golang.org/x/mod v0.16.0
	golang.org/x/sync v0.6.0
)

require (
	github.com/Microsoft/hcsshim v0.11.4 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/go-logr/logr v1.4.1 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/sosodev/duration v1.2.0 // indirect
	go.opentelemetry.io/otel v1.24.0 // indirect
	go.opentelemetry.io/otel/metric v1.24.0 // indirect
	go.opentelemetry.io/otel/trace v1.24.0 // indirect
	golang.org/x/sys v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240325203815-454cdb8f5daa // indirect
	google.golang.org/grpc v1.62.1 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
)
