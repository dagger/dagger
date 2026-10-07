package pb

//go:generate:container dag://go-base
//go:generate:include *.proto
//go:generate protoc -I=. -I=../../../../ --gogofaster_out=. ops.proto
