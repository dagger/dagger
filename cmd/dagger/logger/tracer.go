package logger

import (
	"io"
	"os"

	opentracing "github.com/opentracing/opentracing-go"
	jaeger "github.com/uber/jaeger-client-go"
)

func InitTracing() io.Closer {
	traceAddr := os.Getenv("JAEGER_TRACE")
	if traceAddr == "" {
		return &nopCloser{}
	}

	tr, err := jaeger.NewUDPTransport(traceAddr, 0)
	if err != nil {
		panic(err)
	}

	tracer, closer := jaeger.NewTracer(
		"dagger",
		jaeger.NewConstSampler(true),
		jaeger.NewRemoteReporter(tr),
	)
	opentracing.SetGlobalTracer(tracer)
	return closer
}

type nopCloser struct {
}

func (*nopCloser) Close() error {
	return nil
}
