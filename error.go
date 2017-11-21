package stackdriver

import (
	"fmt"

	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
)

// Errorf reports and error to the error framework
func Errorf(span opentracing.Span, cause error, format string, args ...interface{}) (err error) {
	if cause == nil {
		err = fmt.Errorf(format, args...)
	} else {
		err = errors.Wrapf(err, format, args...)
	}

	s, ok := span.(*Span)
	if !ok {
		return
	}

	s.reportError(err)
	return
}
