package moby_buildkit_v1_frontend //nolint:revive

//go:generate protoc -I=. -I=../../../../../ --gogo_out=Minternal/buildkit/api/types/worker.proto=github.com/dagger/dagger/internal/buildkit/api/types,Minternal/buildkit/solver/pb/ops.proto=github.com/dagger/dagger/internal/buildkit/solver/pb,Minternal/buildkit/sourcepolicy/pb/policy.proto=github.com/dagger/dagger/internal/buildkit/sourcepolicy/pb,Minternal/buildkit/util/apicaps/pb/caps.proto=github.com/dagger/dagger/internal/buildkit/util/apicaps/pb,plugins=grpc:. gateway.proto
