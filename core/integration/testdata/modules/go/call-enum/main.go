package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FromProto(
	// +default="UDP"
	proto dagger.NetworkProtocol,
) string {
	return string(proto)
}

func (m *Test) ToProto(proto string) dagger.NetworkProtocol {
	return dagger.NetworkProtocol(proto)
}
