package moby_buildkit_v1_apicaps //nolint:revive

//go:generate:container dag://go-base
//go:generate:include *.proto
//go:generate protoc -I=. -I=../../../../../ --gogo_out=plugins=grpc:. caps.proto
