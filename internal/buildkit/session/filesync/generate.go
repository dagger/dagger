package filesync

//go:generate protoc -I=. -I=../../../../ --gogoslick_out=plugins=grpc:. filesync.proto
