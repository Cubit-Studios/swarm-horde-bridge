package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

func New() zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339Nano

	return zerolog.New(os.Stdout).
		With().
		Timestamp().
		Caller().
		Logger()
}