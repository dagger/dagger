package types

//go:generate:container dag://go-base
//go:generate:include *.proto
//go:generate protoc -I=. -I=../../../ --gogoslick_out=. stat.proto
