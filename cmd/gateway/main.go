// Command gateway runs one TokenGate gateway instance.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/config"
	"github.com/nishamurthy-22/TokenGate/internal/gateway"
	"github.com/nishamurthy-22/TokenGate/internal/health"
	"github.com/nishamurthy-22/TokenGate/internal/ratelimit"
	"github.com/nishamurthy-22/TokenGate/internal/routing"
)

func main() {
	cfg := config.LoadGateway()

	quotas, err := config.LoadQuotas(cfg.QuotasFile)
	if err != nil {
		log.Fatalf("quotas: %v", err)
	}
	backends, err := config.LoadBackends(cfg.BackendsFile)
	if err != nil {
		log.Fatalf("backends: %v", err)
	}
	policy, err := routing.NewPolicy(cfg.Policy)
	if err != nil {
		log.Fatal(err)
	}
	limiter, err := ratelimit.New(cfg.Limiter, ratelimit.Options{
		Quotas:       quotas,
		RedisAddr:    cfg.RedisAddr,
		GatewayID:    cfg.ID,
		GatewayCount: cfg.GatewayCount,
		ClockSource:  cfg.ClockSource,
		ClockSkew:    cfg.ClockSkew,
	})
	if err != nil {
		log.Fatal(err)
	}

	pool := routing.NewPool(backends)
	gw, err := gateway.New(cfg, quotas, pool, policy, limiter)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.HealthChecks {
		go health.NewChecker(pool, cfg.ID).Run(ctx)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           gw.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("gateway %s on :%s policy=%s limiter=%s fail_mode=%s health_checks=%t retries=%d backends=%d apps=%d",
			cfg.ID, cfg.Port, cfg.Policy, cfg.Limiter, cfg.FailMode, cfg.HealthChecks, cfg.MaxRetries, len(backends), len(quotas))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Println("gateway stopped")
}
