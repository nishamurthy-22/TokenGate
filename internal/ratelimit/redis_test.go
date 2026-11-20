package ratelimit

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/config"
)

// These tests need a running Redis. Run them with:
//
//	docker run -d -p 6379:6379 redis:7-alpine
//	TOKENGATE_TEST_REDIS=localhost:6379 go test ./internal/ratelimit/
func testRedis(t *testing.T, quota config.Quota) (*RedisLimiter, string) {
	t.Helper()
	addr := os.Getenv("TOKENGATE_TEST_REDIS")
	if addr == "" {
		t.Skip("set TOKENGATE_TEST_REDIS to run Redis tests")
	}
	app := fmt.Sprintf("test-%d", time.Now().UnixNano())
	l, err := NewRedis(Options{
		Quotas:    map[string]config.Quota{app: quota},
		RedisAddr: addr,
		GatewayID: "test",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Client().Del(context.Background(), BudgetKey(app)) })
	return l, app
}

func TestRedisNeverOvershoots(t *testing.T) {
	l, app := testRedis(t, config.Quota{Capacity: 1000, RefillPerSec: 0})
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 50; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				ok, err := l.Reserve(context.Background(), app, "", 10)
				if err != nil || !ok {
					return
				}
				admitted.Add(10)
			}
		}()
	}
	wg.Wait()
	if got := admitted.Load(); got != 1000 {
		t.Fatalf("admitted %d tokens, want exactly 1000", got)
	}
}

func TestRedisSettleIsIdempotent(t *testing.T) {
	l, app := testRedis(t, config.Quota{Capacity: 100, RefillPerSec: 0})
	ctx := context.Background()
	reqID := app + "-r1"
	t.Cleanup(func() { l.Client().Del(context.Background(), settledKey(reqID)) })
	if ok, _ := l.Reserve(ctx, app, reqID, 100); !ok {
		t.Fatal("reserve should succeed")
	}
	for i := 0; i < 3; i++ {
		if err := l.Settle(ctx, app, reqID, 100, 40); err != nil {
			t.Fatal(err)
		}
	}
	// Refund of 60 must be applied once: 70 must fail, 60 must succeed.
	if ok, _ := l.Reserve(ctx, app, "r2", 70); ok {
		t.Fatal("refund applied more than once")
	}
	if ok, _ := l.Reserve(ctx, app, "r3", 60); !ok {
		t.Fatal("refund was not applied")
	}
}
