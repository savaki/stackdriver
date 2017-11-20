package stackdriver_test

import (
	"testing"

	"cloud.google.com/go/logging"
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
