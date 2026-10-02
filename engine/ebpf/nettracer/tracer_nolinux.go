//go:build !linux

// Package nettracer accounts for engine and container network traffic.
package nettracer

import "errors"

var errUnsupported = errors.New("eBPF network accounting requires Linux")

type Sample struct {
	InternalRX uint64
	InternalTX uint64
	ExternalRX uint64
	ExternalTX uint64
}

type EngineSample = Sample

type Tracer struct{}

type Attachment struct{}

func New() (*Tracer, error) {
	return nil, errUnsupported
}

func Active() *Tracer {
	return nil
}

func (*Tracer) Close() error {
	return nil
}

func (*Tracer) AttachInterface(string) (*Attachment, error) {
	return nil, errUnsupported
}

func (*Attachment) Sample() (Sample, error) {
	return Sample{}, errUnsupported
}

func (*Attachment) Close() error {
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
