package log

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog"
)

type ctxKey struct{}

var loggerKey = ctxKey{}

// New creates a new zerolog.Logger with JSON output (production style).
func New(level zerolog.Level) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339
	return zerolog.New(os.Stdout).Level(level).With().Timestamp().Logger()
}

// IntoContext stores the logger in a context.
func IntoContext(ctx context.Context, l zerolog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext retrieves the logger from a context.
// If none is found, returns a Nop logger.
func FromContext(ctx context.Context) zerolog.Logger {
	if v := ctx.Value(loggerKey); v != nil {
		if l, ok := v.(zerolog.Logger); ok {
			return l
		}
	}
	return zerolog.Nop()
}
