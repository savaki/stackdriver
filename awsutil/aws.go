package awsutil

import (
	"context"

	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/opentracing/opentracing-go"
)

const (
	awsKey       = "stackdriver.aws"
	headerTarget = "x-amz-target"
)

func Instrument(client *client.Client) {
	client.Handlers.Sign.PushBack(func(req *request.Request) {
		ctx := context.WithValue(req.Context(), awsKey, struct{}{})

		var operationName string
		if values := req.SignedHeaderVals[headerTarget]; len(values) > 0 {
			operationName = values[0]
		} else {
			operationName = req.ClientInfo.ServiceName
		}

		_, child := opentracing.StartSpanFromContext(ctx, operationName)
		req.SetContext(child)
	})

	client.Handlers.Complete.PushBack(func(req *request.Request) {
		ctx := req.Context()
		if ctx.Value(awsKey) == nil {
			return
		}

		span := opentracing.SpanFromContext(ctx)
		span.SetTag("AWSRequestID", req.RequestID)
		span.SetTag("Service", req.ClientInfo.ServiceName)
		if req.HTTPResponse != nil {
			span.SetTag("HTTPResponse", req.HTTPResponse)
		}
		if req.Error != nil {
			span.SetTag("error", req.Error.Error())
		}

		span.Finish()
	})
}
