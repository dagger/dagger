package pb

//go:generate:include *.proto
//go:generate protoc -I=. -I=../../../../ --gogofaster_out=. ops.proto
