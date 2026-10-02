package callpbv1

//go:generate:container dag://go-base
//go:generate:include *.proto
//go:generate protoc --go_out=. --go_opt=paths=source_relative call.proto
