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
	"sync/atomic"
	"time"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/logging"
	"cloud.google.com/go/trace"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"google.golang.org/api/option"
)

const (
	httpHeader  = "X-Stackdriver"
	httpBaggage = "X-Stackdriver-Baggage"
)

// Tracer is a simple, thin interface for Span creation and SpanContext
// propagation.
type Tracer struct {
	traceClient *trace.Client
	errorClient *errorreporting.Client
	logger      Logger
	baggage     map[string]string
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
	span.startedAt = time.Now()
	span.operationName = operationName

	options := opentracing.StartSpanOptions{}
	for _, opt := range opts {
		opt.Apply(&options)
	}

loop:
	for _, ref := range options.References {
		switch ref.Type {
		case opentracing.ChildOfRef, opentracing.FollowsFromRef:
			if parent, ok := ref.ReferencedContext.(*Span); ok {
				if parent.gSpan != nil {
					span.gSpan = parent.gSpan.NewChild(operationName) // local child
					span.errorSent = parent.errorSent

				} else if parent.header != "" {
					span.gSpan = t.traceClient.SpanFromHeader(operationName, parent.header) // remote child
					span.errorSent = parent.errorSent
				}
			}
			break loop
		}
	}

	// create a new span if we don't already have a parent
	if t.traceClient != nil {
		if span.gSpan == nil {
			var req *http.Request
			for _, v := range options.Tags {
				if r, ok := v.(*http.Request); ok && r != nil {
					req = r
					break
				}
			}

			if req != nil {
				span.gSpan = t.traceClient.SpanFromRequest(req)

			} else {
				span.gSpan = t.traceClient.NewSpan(operationName)
			}
		}
	}

	if span.errorSent == nil {
		errorSent := int32(0)
		span.errorSent = &errorSent
	}

	// set root baggage
	for k, v := range t.baggage {
		span.SetBaggageItem(k, v)
	}

	// set baggage from parent span
	for _, ref := range options.References {
		ref.ReferencedContext.ForeachBaggageItem(func(k, v string) bool {
			span.SetBaggageItem(k, v)
			return true
		})
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

	if carrier == nil {
		return opentracing.ErrInvalidCarrier
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
			return opentracing.ErrInvalidCarrier
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
			return opentracing.ErrInvalidCarrier
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
		return opentracing.ErrUnsupportedFormat
	}

	return nil
}

// reportError sends an error to the Stackdriver error reporting service
func (t *Tracer) reportError(err error, errorSent *int32) {
	if err == nil || t.errorClient == nil {
		return
	}

	if v := atomic.AddInt32(errorSent, 1); v == 1 {
		t.errorClient.Report(errorreporting.Entry{
			Error: err,
		})
	}
}

func (t *Tracer) log(content map[string]interface{}, traceID string, baggage, tags map[string]string) {
	if t.logger == nil {
		return
	}

	var labels map[string]string
	if len(baggage)+len(tags) > 0 {
		labels = map[string]string{}
		for k, v := range tags {
			labels[k] = v
		}
		for k, v := range baggage {
			labels[k] = v
		}
	}

	if traceID != "" {
		if labels == nil {
			labels = map[string]string{}
		}
		labels[TagGoogleTraceID] = traceID
	}

	t.logger.Log(logging.Entry{
		Payload: content,
		Labels:  labels,
	})
}

// LogFields allows logging outside the scope of a span
func (t *Tracer) LogFields(fields ...log.Field) {
	content := map[string]interface{}{}
	for _, f := range fields {
		value := f.Value()
		if value == nil {
			continue
		}

		switch v := value.(type) {
		case error:
			var errorSent int32
			t.reportError(v, &errorSent)
			content[f.Key()] = v.Error()

		case fmt.Stringer:
			content[f.Key()] = v.String()

		default:
			content[f.Key()] = v
		}
	}

	t.log(content, "", t.baggage, nil)
}

func extractBinary(carrier interface{}) (string, map[string]string, error) {
	r, ok := carrier.(io.Reader)
	if !ok {
		return "", nil, opentracing.ErrInvalidCarrier
	}
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return "", nil, err
	}
	content := binaryContent{}
	if err := json.Unmarshal(data, &content); err != nil {
		return "", nil, err
	}
	return content.Header, content.Baggage, nil
}

func extractTextMap(carrier interface{}) (string, map[string]string, error) {
	var header string
	var baggage map[string]string

	m, ok := carrier.(opentracing.TextMapReader)
	if !ok {
		return "", nil, opentracing.ErrInvalidCarrier
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
		return "", nil, err
	}

	return header, baggage, nil
}

func extractHTTPHeaders(carrier interface{}) (string, map[string]string, error) {
	var header string
	var baggage map[string]string

	m, ok := carrier.(opentracing.HTTPHeadersCarrier)
	if !ok {
		return "", nil, opentracing.ErrInvalidCarrier
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
		return "", nil, err
	}

	return header, baggage, nil
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
	var err error

	if format == opentracing.Binary {
		header, baggage, err = extractBinary(carrier)
		if err != nil {
			return nil, err
		}

	} else if format == opentracing.TextMap {
		header, baggage, err = extractTextMap(carrier)
		if err != nil {
			return nil, err
		}

	} else if format == opentracing.HTTPHeaders {
		header, baggage, err = extractHTTPHeaders(carrier)
		if err != nil {
			return nil, err
		}

	} else {
		return nil, fmt.Errorf("unhandled format, %v", format)
	}

	errorSent := int32(0)
	span := &Span{
		tracer:    t,
		baggage:   baggage,
		header:    header,
		errorSent: &errorSent,
	}

	return span, nil
}

func (t *Tracer) HTTPHandler(h http.Handler) http.Handler {
	if t.traceClient == nil {
		return h
	}

	return t.traceClient.HTTPHandler(h)
}

// Options contains configuration parameters
type Options struct {
	ErrorClient *errorreporting.Client
	TraceClient *trace.Client
	Logger      Logger
	Baggage     map[string]string
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

// MultiLogger allows logging messages to be sent to multiple loggers
func MultiLogger(loggers ...Logger) Logger {
	return LoggerFunc(func(e logging.Entry) {
		for _, logger := range loggers {
			logger.Log(e)
		}
	})
}

// WithLogger allows the logger to be configured
func WithLogger(logger Logger) Option {
	return optionFunc(func(opt *Options) {
		opt.Logger = logger
	})
}

// WithBaggage allows root baggage to be specified that will propagate to all spans
func WithBaggage(baggage map[string]string) Option {
	return optionFunc(func(opt *Options) {
		opt.Baggage = baggage
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
		baggage:     options.Baggage,
	}
}

// All returns a tracer that includes support for Stackdriver Trace, Logging, and Error Reporting
func All(ctx context.Context, projectID, serviceName, serviceVersion string, opts ...option.ClientOption) (*Tracer, error) {
	b := NewBuilder()
	b.GCP(ctx, projectID, serviceName, serviceVersion, opts...)
	return b.Build()
}
