# stackdriver

[![GoDoc](https://godoc.org/github.com/savaki/stackdriver?status.svg)](https://godoc.org/github.com/savaki/stackdriver)

stackdriver is an opentracing implementation that provides support for Stackdriver Trace, Stackdriver Logging, and 
Stackdriver Error Reporting

## Getting Started

To install stackdriver, use:

```bash
go get -u github.com/savaki/stackdriver
```

## Sample

Assuming a credentials file, ```credentials.json```, the following can be used:

```go
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

	// stackdriver publishes content asynchronous, need to give it a moment 
	time.Sleep(time.Second * 3) 
}
```

### To Do

* Implement Span.FinishWithOptions
* Implement Span.LogKV
