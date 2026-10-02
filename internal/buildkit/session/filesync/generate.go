package filesync

//go:generate:container dag://go-base
//go:generate:include *.proto
//go:generate protoc -I=. -I=../../../../ --gogoslick_out=Minternal/fsutil/types/stat.proto=github.com/dagger/dagger/internal/fsutil/types,plugins=grpc:. filesync.proto
