module dagger

go 1.21.3

require (
	github.com/99designs/gqlgen v0.17.41
	github.com/Khan/genqlient v0.6.0
	github.com/containerd/containerd v1.7.12
	github.com/dagger/dagger v0.9.9
	github.com/moby/buildkit v0.13.0-beta3
	github.com/opencontainers/image-spec v1.1.0-rc5
	github.com/vektah/gqlparser/v2 v2.5.10
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa
	golang.org/x/sync v0.6.0
)

require (
	github.com/Microsoft/hcsshim v0.11.4 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/sosodev/duration v1.1.0 // indirect
	golang.org/x/sys v0.17.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231106174013-bbf56f31fb17 // indirect
	google.golang.org/grpc v1.61.0 // indirect
	google.golang.org/protobuf v1.32.0 // indirect
)

replace github.com/dagger/dagger => ../
