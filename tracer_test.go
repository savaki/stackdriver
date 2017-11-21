package stackdriver_test

import (
	"testing"

	"cloud.google.com/go/logging"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"github.com/savaki/stackdriver"
	"github.com/tj/assert"
)

func TestMultiLogger(t *testing.T) {
	count := 0
	target := stackdriver.LoggerFunc(func(e logging.Entry) {
		count++
	})

	logger := stackdriver.MultiLogger(target, target)
	logger.Log(logging.Entry{})
	assert.Equal(t, 2, count)
}

func TestTracer_LogFields(t *testing.T) {
	received := logging.Entry{}

	builder := stackdriver.NewBuilder()
	builder.LoggerFunc(func(e logging.Entry) {
		received = e
	})
	builder.SetBaggageItem("a", "b")

	tracer, err := builder.Build()
	assert.Nil(t, err)
	tracer.LogFields(log.String("hello", "world"))
	assert.EqualValues(t, map[string]interface{}{"hello": "world"}, received.Payload)
	assert.EqualValues(t, map[string]string{"a": "b"}, received.Labels)
}

func TestWithBaggage(t *testing.T) {
	calls := 0

	builder := stackdriver.NewBuilder()
	builder.LoggerFunc(func(e logging.Entry) {
		calls++
		assert.EqualValues(t, map[string]string{"hello": "world"}, e.Labels)
	})
	builder.SetBaggageItem("hello", "world")
	tracer, err := builder.Build()
	assert.Nil(t, err)

	a := tracer.StartSpan("a")

	b := tracer.StartSpan("b", opentracing.ChildOf(a.Context()))
	b.Finish()

	a.Finish()
	assert.EqualValues(t, 2, calls)
}
