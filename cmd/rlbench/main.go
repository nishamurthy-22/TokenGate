// Command rlbench measures how far a rate limiter overshoots its quota when
// many clients reserve tokens at the same time. Each worker keeps reserving
// until it is rejected; the bucket never refills, so a correct limiter admits
// exactly the capacity and anything above it is overshoot.
//
// Example:
//
//	go run ./cmd/rlbench -redis localhost:6379 -workers 50 -runs 3
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/config"
	"github.com/nishamurthy-22/TokenGate/internal/ratelimit"
)

const benchApp = "bench-app"

func main() {
	redisAddr := flag.String("redis", "localhost:6379", "Redis address")
	workers := flag.Int("workers", 50, "concurrent clients")
	capacity := flag.Float64("capacity", 10000, "bucket capacity in tokens")
	cost := flag.Int("cost", 100, "tokens per reservation")
	runs := flag.Int("runs", 3, "repetitions per limiter")
	limiters := flag.String("limiters", "naive,redis", "comma-separated limiters: naive, redis, local")
	header := flag.Bool("header", true, "print the CSV header line")
	flag.Parse()

	capTokens := *capacity
	quotas := map[string]config.Quota{benchApp: {Capacity: capTokens, RefillPerSec: 0}}

	if *header {
		fmt.Println("limiter,run,workers,capacity,cost,admitted_tokens,overshoot_tokens,overshoot_pct,duration_ms,errors")
	}
	for _, kind := range strings.Split(*limiters, ",") {
		kind = strings.TrimSpace(kind)
		for run := 1; run <= *runs; run++ {
			lim, err := ratelimit.New(kind, ratelimit.Options{
				Quotas:       quotas,
				RedisAddr:    *redisAddr,
				GatewayID:    "rlbench",
				GatewayCount: 1,
				Timeout:      time.Second,
			})
			if err != nil {
				log.Fatal(err)
			}
			if err := reset(lim); err != nil {
				log.Fatalf("could not reset %s budget (is Redis running at %s?): %v", kind, *redisAddr, err)
			}

			admitted, errs, dur := runOnce(lim, *workers, *cost)
			overshoot := float64(admitted) - capTokens
			pct := 100 * overshoot / capTokens
			fmt.Printf("%s,%d,%d,%.0f,%d,%d,%.0f,%.2f,%d,%d\n",
				kind, run, *workers, capTokens, *cost, admitted, overshoot, pct, dur.Milliseconds(), errs)
			closeLimiter(lim)
		}
	}
}

// reset deletes the benchmark budget so every run starts with a full bucket.
func reset(lim ratelimit.Limiter) error {
	ctx := context.Background()
	switch l := lim.(type) {
	case *ratelimit.RedisLimiter:
		return l.Client().Del(ctx, ratelimit.BudgetKey(benchApp)).Err()
	case *ratelimit.NaiveLimiter:
		return l.Client().Del(ctx, ratelimit.NaiveBudgetKey(benchApp)).Err()
	}
	return nil
}

func closeLimiter(lim ratelimit.Limiter) {
	switch l := lim.(type) {
	case *ratelimit.RedisLimiter:
		_ = l.Client().Close()
	case *ratelimit.NaiveLimiter:
		_ = l.Client().Close()
	}
}

func runOnce(lim ratelimit.Limiter, workers, cost int) (admitted, errs int64, dur time.Duration) {
	var adm, failed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errorsSeen := 0
			for {
				ok, err := lim.Reserve(context.Background(), benchApp, "", cost)
				if err != nil {
					failed.Add(1)
					errorsSeen++
					if errorsSeen > 100 {
						return
					}
					continue
				}
				if !ok {
					return
				}
				adm.Add(int64(cost))
			}
		}()
	}
	t0 := time.Now()
	close(start)
	wg.Wait()
	return adm.Load(), failed.Load(), time.Since(t0)
}
