// Package mock implements a mock LLM inference backend. It streams fake tokens
// at a configurable speed and supports fault injection. See docs/DESIGN.md, section 4.
package mock

import (
	"errors"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/config"
)

// Config holds the mock backend settings.
type Config struct {
	ID             string
	Port           string
	TokensPerSec   float64
	TTFT           time.Duration
	OutputMinFrac  float64
	OutputMaxFrac  float64
	Seed           int64
	MaxConcurrency int
	MaxQueue       int
}

// ConfigFromEnv reads the settings from environment variables.
func ConfigFromEnv() Config {
	return Config{
		ID:             config.Env("MOCK_ID", config.Hostname()),
		Port:           config.Env("PORT", "9000"),
		TokensPerSec:   config.EnvFloat("TOKENS_PER_SEC", 50),
		TTFT:           time.Duration(config.EnvInt("TTFT_MS", 200)) * time.Millisecond,
		OutputMinFrac:  config.EnvFloat("OUTPUT_MIN_FRAC", 0.2),
		OutputMaxFrac:  config.EnvFloat("OUTPUT_MAX_FRAC", 1.0),
		Seed:           int64(config.EnvInt("SEED", 42)),
		MaxConcurrency: config.EnvInt("MAX_CONCURRENCY", 32),
		MaxQueue:       config.EnvInt("MAX_QUEUE", 128),
	}
}

// Validate checks that the settings make sense.
func (c Config) Validate() error {
	switch {
	case c.TokensPerSec <= 0:
		return errors.New("TOKENS_PER_SEC must be > 0")
	case c.TTFT < 0:
		return errors.New("TTFT_MS must be >= 0")
	case c.OutputMinFrac <= 0 || c.OutputMaxFrac > 1 || c.OutputMinFrac > c.OutputMaxFrac:
		return errors.New("need 0 < OUTPUT_MIN_FRAC <= OUTPUT_MAX_FRAC <= 1")
	case c.MaxConcurrency < 1:
		return errors.New("MAX_CONCURRENCY must be >= 1")
	case c.MaxQueue < 0:
		return errors.New("MAX_QUEUE must be >= 0")
	}
	return nil
}
