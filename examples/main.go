package main

import (
	"context"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"github.com/savaki/stackdriver"
	"google.golang.org/api/option"
)

func main() {
	ctx := context.Background()
	projectID := "your-gcp-project-id"
	tracer, _ := stackdriver.All(ctx, projectID, "service-name", "version",
		option.WithCredentialsFile("credentials.json"),
	)

	opentracing.SetGlobalTracer(tracer)

	span := opentracing.StartSpan("Sample")
	span.LogFields(log.String("message", "recorded to Stackdriver logging"))
	defer span.Finish()

	time.Sleep(time.Second * 3) // stackdriver publishes content asynchronous, need to give it a moment
}
