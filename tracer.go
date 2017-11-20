// Copyright 2017 Matt Ho
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package stackdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"sync"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/logging"
	"cloud.google.com/go/trace"
	"github.com/opentracing/opentracing-go"
	"google.golang.org/api/option"
)

const (
	httpHeader  = "X-Stackdriver"
	httpBaggage = "X-Stackdriver-Baggage"
)

var (
	optionPool = sync.Pool{
		New: func() interface{} {
			return &opentracing.StartSpanOptions{}
		},
	}
)

// Tracer is a simple, thin interface for Span creation and SpanContext
// propagation.
type Tracer struct {
	traceClient *trace.Client
	errorClient *errorreporting.Client
	logger      Logger
}

// Create, start, and return a new Span with the given `operationName` and
// incorporate the given StartSpanOption `opts`. (Note that `opts` borrows
// from the "functional options" pattern, per
// http://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis)
//
// A Span with no SpanReference options (e.g., opentracing.ChildOf() or
// opentracing.FollowsFrom()) becomes the root of its own trace.
//
// Examples:
//
//     var tracer opentracing.Tracer = ...
//
//     // The root-span case:
//     sp := tracer.StartSpan("GetFeed")
//
//     // The vanilla child span case:
//     sp := tracer.StartSpan(
//         "GetFeed",
//         opentracing.ChildOf(parentSpan.Context()))
//
//     // All the bells and whistles:
//     sp := tracer.StartSpan(
//         "GetFeed",
//         opentracing.ChildOf(parentSpan.Context()),
//         opentracing.Tag{"user_agent", loggedReq.UserAgent},
//         opentracing.StartTime(loggedReq.Timestamp),
//     )
//
func (t *Tracer) StartSpan(operationName string, opts ...opentracing.StartSpanOption) opentracing.Span {
	span := spanPool.Get().(*Span)
	span.tracer = t

	options := optionPool.Get().(*opentracing.StartSpanOptions)
	defer optionPool.Put(options)
	for _, opt := range opts {
		opt.Apply(options)
	}

loop:
	for _, ref := range options.References {
		switch ref.Type {
		case opentracing.ChildOfRef, opentracing.FollowsFromRef:
			if parent, ok := ref.ReferencedContext.(*Span); ok {
				if parent.gSpan != nil {
					if parent.header != "" {
						span.gSpan = t.traceClient.SpanFromHeader(operationName, parent.header)
					} else {
						span.gSpan = parent.gSpan.NewChild(operationName)
					}
				}
			}
			ref.ReferencedContext.ForeachBaggageItem(func(k, v string) bool {
				span.SetBaggageItem(k, v)
				return true
			})
			break loop
		}
	}

	if t.traceClient != nil {
		if span.gSpan == nil {
			span.gSpan = t.traceClient.NewSpan(operationName)
		}
	}

	for k, v := range options.Tags {
		span.SetTag(k, v)
	}

	return span
}

type binaryContent struct {
	Header  string            `json:"h"`
	Baggage map[string]string `json:"b,omitempty"`
}

// Inject() takes the `sm` SpanContext instance and injects it for
// propagation within `carrier`. The actual type of `carrier` depends on
// the value of `format`.
//
// OpenTracing defines a common set of `format` values (see BuiltinFormat),
// and each has an expected carrier type.
//
// Other packages may declare their own `format` values, much like the keys
// used by `context.Context` (see
// https://godoc.org/golang.org/x/net/context#WithValue).
//
// Example usage (sans error handling):
//
//     carrier := opentracing.HTTPHeadersCarrier(httpReq.Header)
//     err := tracer.Inject(
//         span.Context(),
//         opentracing.HTTPHeaders,
//         carrier)
//
// NOTE: All opentracing.Tracer implementations MUST support all
// BuiltinFormats.
//
// Implementations may return opentracing.ErrUnsupportedFormat if `format`
// is not supported by (or not known by) the implementation.
//
// Implementations may return opentracing.ErrInvalidCarrier or any other
// implementation-specific error if the format is supported but injection
// fails anyway.
//
// See Tracer.Extract().
func (t *Tracer) Inject(sm opentracing.SpanContext, format interface{}, carrier interface{}) error {
	span, ok := sm.(*Span)
	if !ok {
		return fmt.Errorf("unsupported SpanContext, %v", sm)
	}

	var header string
	if span.gSpan != nil {
		req, _ := http.NewRequest(http.MethodGet, "http://localhost", nil)
		span.gSpan.NewRemoteChild(req)

	loop:
		for _, values := range req.Header {
			for _, v := range values {
				header = v
				break loop
			}
		}
	}

	if header == "" {
		return nil
	}

	if format == opentracing.Binary {
		w, ok := carrier.(io.Writer)
		if !ok {
			return fmt.Errorf("requires an io.Writer carrier")
		}
		content := binaryContent{
			Header:  header,
			Baggage: span.baggage,
		}
		data, err := json.Marshal(content)
		if err != nil {
			return err
		}
		io.WriteString(w, string(data))

	} else if format == opentracing.TextMap || format == opentracing.HTTPHeaders {
		m, ok := carrier.(opentracing.TextMapWriter)
		if !ok {
			return fmt.Errorf("requires an opentracing.TextMapWriter")
		}
		m.Set(httpHeader, header)

		if span.baggage != nil {
			data, err := json.Marshal(span.baggage)
			if err != nil {
				return err
			}
			m.Set(httpBaggage, string(data))
		}

	} else {
		return fmt.Errorf("unhandled format, %v", format)
	}
	return nil
}

// Extract() returns a SpanContext instance given `format` and `carrier`.
//
// OpenTracing defines a common set of `format` values (see BuiltinFormat),
// and each has an expected carrier type.
//
// Other packages may declare their own `format` values, much like the keys
// used by `context.Context` (see
// https://godoc.org/golang.org/x/net/context#WithValue).
//
// Example usage (with StartSpan):
//
//
//     carrier := opentracing.HTTPHeadersCarrier(httpReq.Header)
//     clientContext, err := tracer.Extract(opentracing.HTTPHeaders, carrier)
//
//     // ... assuming the ultimate goal here is to resume the trace with a
//     // server-side Span:
//     var serverSpan opentracing.Span
//     if err == nil {
//         span = tracer.StartSpan(
//             rpcMethodName, ext.RPCServerOption(clientContext))
//     } else {
//         span = tracer.StartSpan(rpcMethodName)
//     }
//
//
// NOTE: All opentracing.Tracer implementations MUST support all
// BuiltinFormats.
//
// Return values:
//  - A successful Extract returns a SpanContext instance and a nil error
//  - If there was simply no SpanContext to extract in `carrier`, Extract()
//    returns (nil, opentracing.ErrSpanContextNotFound)
//  - If `format` is unsupported or unrecognized, Extract() returns (nil,
//    opentracing.ErrUnsupportedFormat)
//  - If there are more fundamental problems with the `carrier` object,
//    Extract() may return opentracing.ErrInvalidCarrier,
//    opentracing.ErrSpanContextCorrupted, or implementation-specific
//    errors.
//
// See Tracer.Inject().
func (t *Tracer) Extract(format interface{}, carrier interface{}) (opentracing.SpanContext, error) {
	var header string
	var baggage map[string]string

	if format == opentracing.Binary {
		r, ok := carrier.(io.Reader)
		if !ok {
			return nil, fmt.Errorf("requires an io.Reader carrier")
		}
		data, err := ioutil.ReadAll(r)
		if err != nil {
			return nil, err
		}
		content := binaryContent{}
		if err := json.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		header = content.Header
		baggage = content.Baggage

	} else if format == opentracing.TextMap {
		m, ok := carrier.(opentracing.TextMapReader)
		if !ok {
			return nil, fmt.Errorf("requires an opentracing.TextMapWriter")
		}
		fn := func(k, v string) error {
			if k == httpHeader {
				header = v
			} else if k == httpBaggage {
				return json.Unmarshal([]byte(v), &baggage)
			}
			return nil
		}

		if err := m.ForeachKey(fn); err != nil {
			return nil, err
		}

	} else if format == opentracing.HTTPHeaders {
		m, ok := carrier.(opentracing.HTTPHeadersCarrier)
		if !ok {
			return nil, fmt.Errorf("requires an opentracing.TextMapWriter")
		}
		fn := func(k, v string) error {
			if k == httpHeader {
				header = v
			} else if k == httpBaggage {
				return json.Unmarshal([]byte(v), &baggage)
			}
			return nil
		}

		if err := m.ForeachKey(fn); err != nil {
			return nil, err
		}

	} else {
		return nil, fmt.Errorf("unhandled format, %v", format)
	}

	gSpan := t.traceClient.SpanFromHeader("child", header)
	for k, v := range baggage {
		gSpan.SetLabel(k, v)
	}

	span := &Span{
		tracer:  t,
		baggage: baggage,
		gSpan:   gSpan,
		header:  header,
	}

	return span, nil
}

// Options contains configuration parameters
type Options struct {
	ErrorClient *errorreporting.Client
	TraceClient *trace.Client
	Logger      Logger
}

// Option defines a functional configuration
type Option interface {
	Apply(*Options)
}

type optionFunc func(*Options)

func (fn optionFunc) Apply(opt *Options) {
	fn(opt)
}

// WithErrorClient allows the error client to be optional specified
func WithErrorClient(client *errorreporting.Client) Option {
	return optionFunc(func(opt *Options) {
		opt.ErrorClient = client
	})
}

// WithTraceClient allows the trace client to be optional specified
func WithTraceClient(client *trace.Client) Option {
	return optionFunc(func(opt *Options) {
		opt.TraceClient = client
	})
}

// Logger allows the logger to be specified
type Logger interface {
	Log(e logging.Entry)
}

// LoggerFunc provides a functional adapter to Logger
type LoggerFunc func(e logging.Entry)

// Log implements Logger
func (fn LoggerFunc) Log(e logging.Entry) {
	fn(e)
}

// WithLogger allows the logger to be configured
func WithLogger(logger Logger) Option {
	return optionFunc(func(opt *Options) {
		opt.Logger = logger
	})
}

// New constructs a new stackdriver tracer
func New(opts ...Option) *Tracer {
	options := &Options{}
	for _, opt := range opts {
		opt.Apply(options)
	}

	return &Tracer{
		traceClient: options.TraceClient,
		errorClient: options.ErrorClient,
		logger:      options.Logger,
	}
}

// All returns a tracer that includes support for Stackdriver Trace, Logging, and Error Reporting
func All(ctx context.Context, projectID, serviceName, serviceVersion string, opts ...option.ClientOption) (*Tracer, error) {
	errorClient, err := errorreporting.NewClient(ctx, projectID, errorreporting.Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
	}, opts...)
	if err != nil {
		return nil, err
	}

	loggingClient, err := logging.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, err
	}
	logger := loggingClient.Logger(serviceName)

	traceClient, err := trace.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, err
	}

	return New(WithErrorClient(errorClient), WithTraceClient(traceClient), WithLogger(logger)), nil
}
