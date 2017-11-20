package zaplog

import (
	"cloud.google.com/go/logging"
	"github.com/savaki/stackdriver"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	message = "message"
)

// Wrap a zap logger into a stackdriver.Logger
func Wrap(logger *zap.Logger) stackdriver.Logger {
	return stackdriver.LoggerFunc(func(e logging.Entry) {
		content, ok := e.Payload.(map[string]interface{})
		if !ok {
			return
		}

		fields := make([]zapcore.Field, 0, len(content)+len(e.Labels))
		var msg string
		for k, v := range content {
			if k == message {
				if v, ok := v.(string); ok {
					msg = v
				}
				continue
			}

			switch value := v.(type) {
			case bool:
				fields = append(fields, zap.Bool(k, value))
			case string:
				fields = append(fields, zap.String(k, value))
			case float32:
				fields = append(fields, zap.Float32(k, value))
			case float64:
				fields = append(fields, zap.Float64(k, value))
			case int:
				fields = append(fields, zap.Int(k, value))
			case int8:
				fields = append(fields, zap.Int8(k, value))
			case int16:
				fields = append(fields, zap.Int16(k, value))
			case int32:
				fields = append(fields, zap.Int32(k, value))
			case int64:
				fields = append(fields, zap.Int64(k, value))
			case uint:
				fields = append(fields, zap.Uint(k, value))
			case uint8:
				fields = append(fields, zap.Uint8(k, value))
			case uint16:
				fields = append(fields, zap.Uint16(k, value))
			case uint32:
				fields = append(fields, zap.Uint32(k, value))
			case uint64:
				fields = append(fields, zap.Uint64(k, value))
			}
		}

		for k, v := range e.Labels {
			fields = append(fields, zap.String(k, v))
		}

		logger.Info(msg, fields...)
	})
}
