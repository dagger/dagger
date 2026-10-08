//go:build !linux || (!386 && !amd64 && !arm64)

// Package nettracer accounts for engine and container network traffic.
package nettracer

import "errors"

var errUnsupported = errors.New("eBPF network accounting requires Linux on x86 or arm64")

type Sample struct {
	InternalRX uint64
	InternalTX uint64
	ExternalRX uint64
	ExternalTX uint64
}

type EngineSample = Sample

type Tracer struct{}

type Workload struct{}

func New() (*Tracer, error) {
	return nil, errUnsupported
}

func Active() *Tracer {
	return nil
}

func (*Tracer) Close() error {
	return nil
}

func WorkloadParentPath(string) (string, bool) {
	return "", false
}

func (*Tracer) AttachWorkloads(string) error {
	return errUnsupported
}

func (*Tracer) AddInternalPrefixesForVeth(string) error {
	return errUnsupported
}

func CurrentNetnsCookie() (uint64, error) {
	return 0, errUnsupported
}

func (*Tracer) Workload(string, uint64) (*Workload, error) {
	return nil, errUnsupported
}

func (*Workload) Sample() (Sample, error) {
	return Sample{}, errUnsupported
}

func (*Workload) Close() error {
	return nil
}

func EngineAccountingAvailable() bool {
	return false
}

func EngineAccountingError() error {
	return errUnsupported
}

func SampleEngine() (EngineSample, error) {
	return EngineSample{}, errUnsupported
}
