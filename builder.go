package stackdriver

import (
	"context"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/logging"
	"cloud.google.com/go/trace"
	"google.golang.org/api/option"
)

// Builder implements a fluent build to construct a stackdriver Tracer
type Builder struct {
	loggers     []Logger
	traceClient *trace.Client
	errorClient *errorreporting.Client
	baggage     map[string]string
	err         error
}

// Loggers appends to the list of loggers the Tracer will output to
func (b *Builder) Loggers(loggers ...Logger) *Builder {
	b.loggers = append(b.loggers, loggers...)
	return b
}

// LoggerFuncs appends to the list of loggers the Tracer will output to
func (b *Builder) LoggerFunc(fn LoggerFunc) *Builder {
	b.loggers = append(b.loggers, fn)
	return b
}

// TraceClient specifies the *trace.Client to use
func (b *Builder) TraceClient(client *trace.Client) *Builder {
	b.traceClient = client
	return b
}

// ErrorClient specifies the *errorreporting.Client to use
func (b *Builder) ErrorClient(client *errorreporting.Client) *Builder {
	b.errorClient = client
	return b
}

// SetBaggageItem specifies root baggage that will be propagated to all spans
func (b *Builder) SetBaggageItem(key, value string) *Builder {
	if b.baggage == nil {
		b.baggage = map[string]string{}
	}
	b.baggage[key] = value
	return b
}

// GCP provides a utility that registers the GCP trace, error, and logging clients
func (b *Builder) GCP(ctx context.Context, projectID, serviceName, serviceVersion string, opts ...option.ClientOption) *Builder {
	traceClient, err := trace.NewClient(ctx, projectID, opts...)
	if err != nil {
		b.err = err
		return b
	}

	errorClient, err := errorreporting.NewClient(ctx, projectID, errorreporting.Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
	}, opts...)
	if err != nil {
		b.err = err
		return b
	}

	loggingClient, err := logging.NewClient(ctx, projectID, opts...)
	if err != nil {
		b.err = err
		return b
	}
	logger := loggingClient.Logger(serviceName)

	b.traceClient = traceClient
	b.errorClient = errorClient
	b.loggers = append(b.loggers, logger)

	return b
}

// Build constructs the Tracer with the options provided
func (b *Builder) Build() (*Tracer, error) {
	if err := b.err; err != nil {
		return nil, err
	}

	var options []Option
	if b.loggers != nil {
		switch len(b.loggers) {
		case 0:
			// intentionally left blank
		case 1:
			options = append(options, WithLogger(b.loggers[0]))
		default:
			options = append(options, WithLogger(MultiLogger(b.loggers...)))
		}
	}
	if b.traceClient != nil {
		options = append(options, WithTraceClient(b.traceClient))
	}
	if b.errorClient != nil {
		options = append(options, WithErrorClient(b.errorClient))
	}
	if b.baggage != nil {
		options = append(options, WithBaggage(b.baggage))
	}

	return New(options...), nil
}

// NewBuilder constructs a new fluent builder
func NewBuilder() *Builder {
	return &Builder{}
}
