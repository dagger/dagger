package contenthash

//go:generate:include *.proto
//go:generate protoc -I=. -I=../../../../ --gogofaster_out=. checksum.proto
