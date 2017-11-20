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
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"

	"cloud.google.com/go/logging"
	"cloud.google.com/go/trace"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
)

var (
	spanPool = sync.Pool{
		New: func() interface{} {
			return &Span{}
		},
	}
)

// Span references a dapper Span
type Span struct {
	tracer  *Tracer
	baggage map[string]string
	tags    map[string]string
	sampled bool
	gSpan   *trace.Span
	header  string
	resp    *http.Response
}

func (s *Span) release() {
	for key := range s.baggage {
		delete(s.baggage, key)
	}
	for key := range s.tags {
		delete(s.tags, key)
	}

	s.sampled = false
	s.gSpan = nil
	s.header = ""
	s.resp = nil

	spanPool.Put(s)
}

// ForeachBaggageItem implements SpanContext
func (s *Span) ForeachBaggageItem(handler func(k, v string) bool) {
	for k, v := range s.baggage {
		if !handler(k, v) {
			return
		}
	}
}

// Sets the end timestamp and finalizes *Span state.
//
// With the exception of calls to Context() (which are always allowed),
// Finish() must be the last call made to any span instance, and to do
// otherwise leads to undefined behavior.
func (s *Span) Finish() {
	s.FinishWithOptions(opentracing.FinishOptions{})
}

// FinishWithOptions is like Finish() but with explicit control over
// timestamps and log data.
func (s *Span) FinishWithOptions(opts opentracing.FinishOptions) {
	if s.gSpan != nil {
		if s.resp != nil {
			s.gSpan.Finish(trace.WithResponse(s.resp))

		} else {
			s.gSpan.Finish()
		}
	}

	defer s.release()
}

// Context() yields the SpanContext for this *Span. Note that the return
// value of Context() is still valid after a call to Span.Finish(), as is
// a call to Span.Context() after a call to Span.Finish().
func (s *Span) Context() opentracing.SpanContext {
	return s
}

// Sets or changes the operation name.
func (s *Span) SetOperationName(operationName string) opentracing.Span {
	fmt.Fprintln(os.Stderr, "stackdriver does not support SetOperationName")
	return s
}

// Adds a tag to the span.
//
// If there is a pre-existing tag set for `key`, it is overwritten.
//
// Tag values can be numeric types, strings, or bools. The behavior of
// other tag value types is undefined at the OpenTracing level. If a
// tracing system does not know how to handle a particular value type, it
// may ignore the tag, but shall not panic.
func (s *Span) SetTag(key string, value interface{}) opentracing.Span {
	if s.tags == nil {
		s.tags = map[string]string{}
	}

	var str string
	switch v := value.(type) {
	case *http.Response:
		s.resp = v
	case string:
		str = v
	case bool:
		str = strconv.FormatBool(v)
	case int:
		str = strconv.FormatInt(int64(v), 10)
	case int8:
		str = strconv.FormatInt(int64(v), 10)
	case int16:
		str = strconv.FormatInt(int64(v), 10)
	case int32:
		str = strconv.FormatInt(int64(v), 10)
	case int64:
		str = strconv.FormatInt(int64(v), 10)
	case uint:
		str = strconv.FormatUint(uint64(v), 10)
	case uint8:
		str = strconv.FormatUint(uint64(v), 10)
	case uint16:
		str = strconv.FormatUint(uint64(v), 10)
	case uint32:
		str = strconv.FormatUint(uint64(v), 10)
	case uint64:
		str = strconv.FormatUint(uint64(v), 10)
	case float64:
		str = strconv.FormatFloat(v, 'f', 2, 64)
	case float32:
		str = strconv.FormatFloat(float64(v), 'f', 2, 32)
	default:
		str = fmt.Sprintf("%v", value)
	}

	s.tags[key] = str
	if s.gSpan != nil {
		s.gSpan.SetLabel(key, str)
	}

	return s
}

// LogFields is an efficient and type-checked way to record key:value
// logging data about a Span, though the programming interface is a little
// more verbose than LogKV(). Here's an example:
//
//    span.LogFields(
//        log.String("event", "soft error"),
//        log.String("type", "cache timeout"),
//        log.Int("waited.millis", 1500))
//
// Also see Span.FinishWithOptions() and FinishOptions.BulkLogData.
func (s *Span) LogFields(fields ...log.Field) {
	if s.tracer.logger == nil {
		return
	}

	var labels map[string]string
	if len(s.baggage)+len(s.tags) > 0 {
		labels = map[string]string{}
		for k, v := range s.tags {
			labels[k] = v
		}
		for k, v := range s.baggage {
			labels[k] = v
		}
	}

	content := map[string]interface{}{}
	for _, f := range fields {
		content[f.Key()] = f.Value()
	}

	var traceID string
	if s.gSpan != nil {
		if labels == nil {
			labels = map[string]string{}
		}
		labels["appengine.googleapis.com/trace_id"] = s.gSpan.TraceID()
	}

	s.tracer.logger.Log(logging.Entry{
		Payload: content,
		Labels:  labels,
		Trace:   traceID,
	})
}

// LogKV is a concise, readable way to record key:value logging data about
// a Span, though unfortunately this also makes it less efficient and less
// type-safe than LogFields(). Here's an example:
//
//    span.LogKV(
//        "event", "soft error",
//        "type", "cache timeout",
//        "waited.millis", 1500)
//
// For LogKV (as opposed to LogFields()), the parameters must appear as
// key-value pairs, like
//
//    span.LogKV(key1, val1, key2, val2, key3, val3, ...)
//
// The keys must all be strings. The values may be strings, numeric types,
// bools, Go error instances, or arbitrary structs.
//
// (Note to implementors: consider the log.InterleavedKVToFields() helper)
func (s *Span) LogKV(alternatingKeyValues ...interface{}) {
	if s.tracer.logger == nil {
		return
	}
}

// SetBaggageItem sets a key:value pair on this *Span and its *SpanContext
// that also propagates to descendants of this *Span.
//
// SetBaggageItem() enables powerful functionality given a full-stack
// opentracing integration (e.g., arbitrary application data from a mobile
// app can make it, transparently, all the way into the depths of a storage
// system), and with it some powerful costs: use this feature with care.
//
// IMPORTANT NOTE #1: SetBaggageItem() will only propagate baggage items to
// *future* causal descendants of the associated Span.
//
// IMPORTANT NOTE #2: Use this thoughtfully and with care. Every key and
// value is copied into every local *and remote* child of the associated
// Span, and that can add up to a lot of network and cpu overhead.
//
// Returns a reference to this *Span for chaining.
func (s *Span) SetBaggageItem(restrictedKey, value string) opentracing.Span {
	if s.baggage == nil {
		s.baggage = map[string]string{}
	}
	s.baggage[restrictedKey] = value

	if s.gSpan != nil {
		s.gSpan.SetLabel(restrictedKey, value)
	}

	return s
}

// Gets the value for a baggage item given its key. Returns the empty string
// if the value isn't found in this *Span.
func (s *Span) BaggageItem(restrictedKey string) string {
	return s.baggage[restrictedKey]
}

// Provides access to the Tracer that created this *Span.
func (s *Span) Tracer() opentracing.Tracer {
	return s.tracer
}

// Deprecated: use LogFields or LogKV
func (s *Span) LogEvent(event string) {
	fmt.Fprintln(os.Stderr, "Span.LogEvent is deprecated. Use LogFields or LogKV")
}

// Deprecated: use LogFields or LogKV
func (s *Span) LogEventWithPayload(event string, payload interface{}) {
	fmt.Fprintln(os.Stderr, "Span.LogEventWithPayload is deprecated. Use LogFields or LogKV")
}

// Deprecated: use LogFields or LogKV
func (s *Span) Log(data opentracing.LogData) {
	fmt.Fprintln(os.Stderr, "Span.Log is deprecated. Use LogFields or LogKV")
}
