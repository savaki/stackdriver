package awsutil_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/opentracing/opentracing-go"
	"github.com/savaki/stackdriver"
	"github.com/savaki/stackdriver/awsutil"
	"github.com/tj/assert"
	"google.golang.org/api/option"
)

const (
	credentialsFile = "../credentials.json"
)

func TestWrap(t *testing.T) {
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		t.SkipNow()
	}
	if _, err := os.Stat(credentialsFile); err != nil {
		t.SkipNow()
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" || os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		t.SkipNow()
	}

	ctx := context.Background()
	tracer, err := stackdriver.All(ctx, projectID, "service", "latest", option.WithCredentialsFile(credentialsFile))
	assert.Nil(t, err)

	opentracing.InitGlobalTracer(tracer)

	s, err := session.NewSession(&aws.Config{Region: aws.String("us-west-2")})
	assert.Nil(t, err)

	api := dynamodb.New(s)
	awsutil.Instrument(api.Client)

	span, ctx := opentracing.StartSpanFromContext(ctx, "apiCall")

	time.Sleep(time.Millisecond * 128)
	_, err = api.ListTablesWithContext(ctx, &dynamodb.ListTablesInput{})
	assert.Nil(t, err)
	time.Sleep(time.Millisecond * 85)

	span.Finish()

	time.Sleep(time.Second * 5)
}
