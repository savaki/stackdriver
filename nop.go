package stackdriver

import (
	"cloud.google.com/go/logging"
	"github.com/opentracing/opentracing-go"
)

var (
	// Nop tracer that does absolutely nothing beyond implementing opentracing.Tracer
	Nop opentracing.Tracer
)

func init() {
	stdout := NewBuilder()
	stdout.LoggerFunc(func(e logging.Entry) {

	})
	Nop, _ = stdout.Build()
}
