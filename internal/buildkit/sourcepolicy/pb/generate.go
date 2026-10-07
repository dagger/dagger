package moby_buildkit_v1_sourcepolicy //nolint:revive

//go:generate:container dag://go-base
//go:generate:include *.proto
//go:generate protoc -I=. --gogofaster_out=plugins=grpc:. policy.proto
