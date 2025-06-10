module github.com/dagger/dagger/cmd/engine/.dagger

go 1.23.8

require (
	github.com/dagger/dagger/engine/distconsts v0.18.5
	github.com/dagger/dagger/sdk/typescript/runtime v0.15.3
)

replace (
	github.com/dagger/dagger/engine/distconsts => ../../../engine/distconsts
	github.com/dagger/dagger/sdk/typescript/runtime => ../../../sdk/typescript/runtime
)

require (
	github.com/99designs/gqlgen v0.17.74
	github.com/Khan/genqlient v0.8.1
	github.com/containerd/platforms v1.0.0-rc.1
	github.com/moby/buildkit v0.21.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/vektah/gqlparser/v2 v2.5.27
	go.opentelemetry.io/otel v1.35.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.8.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.8.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.32.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.32.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.32.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.32.0
	go.opentelemetry.io/otel/log v0.8.0
	go.opentelemetry.io/otel/metric v1.35.0
	go.opentelemetry.io/otel/sdk v1.35.0
	go.opentelemetry.io/otel/sdk/log v0.8.0
	go.opentelemetry.io/otel/sdk/metric v1.35.0
	go.opentelemetry.io/otel/trace v1.35.0
	go.opentelemetry.io/proto/otlp v1.3.1
	golang.org/x/exp v0.0.0-20250408133849-7e4ce0ab07d0
	golang.org/x/sync v0.15.0
	google.golang.org/grpc v1.73.0
)

require (
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.23.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.32.0 // indirect
	golang.org/x/net v0.41.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.26.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250324211829-b45e905df463 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250324211829-b45e905df463 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
)

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.8.0

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.8.0

replace go.opentelemetry.io/otel/log => go.opentelemetry.io/otel/log v0.8.0

replace go.opentelemetry.io/otel/sdk/log => go.opentelemetry.io/otel/sdk/log v0.8.0
