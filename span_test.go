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

package stackdriver_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/logging"
	"cloud.google.com/go/trace"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"github.com/savaki/stackdriver"
	"github.com/tj/assert"
	"google.golang.org/api/option"
)

const (
	credentialsFile = "credentials.json"
)

func TestBaggage(t *testing.T) {
	tracer := stackdriver.New()
	a := tracer.StartSpan("a")
	defer a.Finish()
	a.SetBaggageItem("hello", "world")

	b := tracer.StartSpan("b", opentracing.ChildOf(a.Context()))
	defer b.Finish()

	assert.EqualValues(t, "world", b.BaggageItem("hello"))
}

func TestSpan_SetBaggageItem(t *testing.T) {
	var entry logging.Entry
	fn := stackdriver.LoggerFunc(func(e logging.Entry) {
		entry = e
	})

	tracer := stackdriver.New(stackdriver.WithLogger(fn))
	a := tracer.StartSpan("a")
	defer a.Finish()

	a.SetBaggageItem("key", "value")
	a.LogFields()

	assert.EqualValues(t, "value", entry.Labels["key"])
}

func TestSpan_SetTag(t *testing.T) {
	var entry logging.Entry
	fn := stackdriver.LoggerFunc(func(e logging.Entry) {
		entry = e
	})

	tracer := stackdriver.New(stackdriver.WithLogger(fn))
	a := tracer.StartSpan("a")
	defer a.Finish()

	a.SetTag("hello", "world")
	a.LogFields()

	assert.EqualValues(t, "world", entry.Labels["hello"])
}

func TestSpan(t *testing.T) {
	tracer := stackdriver.New()
	a := tracer.StartSpan("a")
	defer a.Finish()

	b := tracer.StartSpan("b", opentracing.ChildOf(a.Context()))
	defer b.Finish()
}

func TestOpentracing(t *testing.T) {
	tracer := stackdriver.New()
	opentracing.SetGlobalTracer(tracer)

	a, ctx := opentracing.StartSpanFromContext(context.Background(), "a")
	defer a.Finish()

	b, ctx := opentracing.StartSpanFromContext(ctx, "b")
	defer b.Finish()
}

func BenchmarkSpan(t *testing.B) {
	tracer := stackdriver.New()

	for i := 0; i < t.N; i++ {
		a := tracer.StartSpan("a")
		a.Finish()
	}
}

func TestTrace(t *testing.T) {
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		t.SkipNow()
	}
	if _, err := os.Stat(credentialsFile); err != nil {
		t.SkipNow()
	}

	ctx := context.Background()
	client, err := trace.NewClient(ctx, projectID, option.WithCredentialsFile(credentialsFile))
	assert.Nil(t, err)

	tracer := stackdriver.New(stackdriver.WithTraceClient(client))
	span := tracer.StartSpan("Sample")
	span.SetTag("hello", "world")
	span.Finish()

	time.Sleep(time.Second * 3)
}

func TestLog(t *testing.T) {
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		t.SkipNow()
	}
	if _, err := os.Stat(credentialsFile); err != nil {
		t.SkipNow()
	}

	ctx := context.Background()
	client, err := logging.NewClient(ctx, projectID, option.WithCredentialsFile(credentialsFile))
	assert.Nil(t, err)
	logger := client.Logger(projectID)

	tracer := stackdriver.New(stackdriver.WithLogger(logger))
	span := tracer.StartSpan("Sample")
	span.SetTag("hello", "world")
	span.LogFields(log.String("message", fmt.Sprintf("howdy! %v", time.Now())))
	span.Finish()

	time.Sleep(time.Second * 3)
}

func TestAll(t *testing.T) {
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		t.SkipNow()
	}
	if _, err := os.Stat(credentialsFile); err != nil {
		t.SkipNow()
	}

	ctx := context.Background()
	tracer, err := stackdriver.All(ctx, projectID, "service", "latest", option.WithCredentialsFile(credentialsFile))
	assert.Nil(t, err)

	span := tracer.StartSpan("Sample")
	span.SetTag("hello", "world")
	span.LogFields(log.String("message", fmt.Sprintf("traced! %v", time.Now())))
	span.Finish()

	time.Sleep(time.Second * 3)
}
