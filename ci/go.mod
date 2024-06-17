module github.com/dagger/dagger/ci

go 1.22

require github.com/dagger/dagger/engine/distconsts v0.11.6

replace github.com/dagger/dagger/engine/distconsts => ../engine/distconsts

require (
	github.com/99designs/gqlgen v0.17.44
	github.com/Khan/genqlient v0.7.0
	github.com/containerd/containerd v1.7.15
	github.com/magefile/mage v1.15.0
	github.com/moby/buildkit v0.13.0-rc3.0.20240501212635-51d85d712fad // https://github.com/moby/buildkit/commit/51d85d712fad213cd10ac362b18c0a5aab909923
	github.com/opencontainers/image-spec v1.1.0
	github.com/vektah/gqlparser/v2 v2.5.11
	go.opentelemetry.io/otel v1.27.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.0.0-20240616151512-499785e13488
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.3.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.26.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.26.0
	go.opentelemetry.io/otel/log v0.3.0
	go.opentelemetry.io/otel/sdk v1.27.0
	go.opentelemetry.io/otel/sdk/log v0.3.0
	go.opentelemetry.io/otel/trace v1.27.0
	go.opentelemetry.io/proto/otlp v1.2.0
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842
	golang.org/x/mod v0.17.0
	golang.org/x/sync v0.7.0
	google.golang.org/grpc v1.64.0
)

require (
	github.com/Microsoft/hcsshim v0.11.4 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.20.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/sosodev/duration v1.3.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.26.0 // indirect
	go.opentelemetry.io/otel/metric v1.27.0 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.21.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240520151616-dc85e6b867a5 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240610135401-a8a62080eff3 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
)
