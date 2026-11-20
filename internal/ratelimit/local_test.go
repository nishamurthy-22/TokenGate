package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/nishamurthy-22/TokenGate/internal/config"
)

func TestLocalReserveAndSettle(t *testing.T) {
	ctx := context.Background()
	l := NewLocal(map[string]config.Quota{"app": {Capacity: 100, RefillPerSec: 0}}, 1)

	if ok, _ := l.Reserve(ctx, "app", "r1", 60); !ok {
		t.Fatal("first reservation should succeed")
	}
	if ok, _ := l.Reserve(ctx, "app", "r2", 60); ok {
		t.Fatal("second reservation should fail: only 40 left")
	}
	if err := l.Settle(ctx, "app", "r1", 60, 10); err != nil {
		t.Fatal(err)
	}
	if ok, _ := l.Reserve(ctx, "app", "r3", 60); !ok {
		t.Fatal("after refunding 50, reservation of 60 should succeed")
	}
}

func TestLocalSplitsBudget(t *testing.T) {
	l := NewLocal(map[string]config.Quota{"app": {Capacity: 100}}, 2)
	if ok, _ := l.Reserve(context.Background(), "app", "r1", 60); ok {
		t.Fatal("with 2 gateways each holds only 50 tokens")
	}
}

func TestLocalRefills(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLocal(map[string]config.Quota{"app": {Capacity: 100, RefillPerSec: 10}}, 1)
	l.now = func() time.Time { return now }
	ctx := context.Background()

	if ok, _ := l.Reserve(ctx, "app", "r1", 100); !ok {
		t.Fatal("full bucket should allow 100")
	}
	now = now.Add(3 * time.Second)
	if ok, _ := l.Reserve(ctx, "app", "r2", 30); !ok {
		t.Fatal("3 s at 10 tokens/s should allow 30")
	}
	if ok, _ := l.Reserve(ctx, "app", "r3", 1); ok {
		t.Fatal("bucket should be empty")
	}
}

func TestLocalUnknownApp(t *testing.T) {
	l := NewLocal(map[string]config.Quota{}, 1)
	if _, err := l.Reserve(context.Background(), "nope", "r1", 1); err == nil {
		t.Fatal("expected error for unknown app")
	}
}
