// Command mockbackend runs a mock LLM inference backend that streams tokens.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/mock"
)

func main() {
	cfg := mock.ConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mock.NewServer(cfg).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("mock backend %s on :%s (%.0f tokens/s, TTFT %s, concurrency %d)",
			cfg.ID, cfg.Port, cfg.TokensPerSec, cfg.TTFT, cfg.MaxConcurrency)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Println("mock backend stopped")
}
