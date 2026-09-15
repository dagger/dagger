package errdefs

//go:generate protoc -I=. -I=../../../../ --gogo_out=Minternal/buildkit/solver/pb/ops.proto=github.com/dagger/dagger/internal/buildkit/solver/pb:. errdefs.proto
