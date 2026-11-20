package routing

import (
	"testing"

	"github.com/nishamurthy-22/TokenGate/internal/config"
)

func testPool() *Pool {
	return NewPool([]config.BackendConfig{
		{ID: "a", URL: "http://a"},
		{ID: "b", URL: "http://b"},
		{ID: "c", URL: "http://c"},
	})
}

func TestRoundRobinCycles(t *testing.T) {
	p := &RoundRobin{}
	bs := testPool().All()
	want := []string{"a", "b", "c", "a"}
	for i, w := range want {
		if got := p.Pick(bs).ID; got != w {
			t.Fatalf("pick %d: got %s, want %s", i, got, w)
		}
	}
}

func TestLeastRequestsPicksLowest(t *testing.T) {
	bs := testPool().All()
	bs[0].OpenRequests.Store(5)
	bs[1].OpenRequests.Store(1)
	bs[2].OpenRequests.Store(3)
	p := &LeastRequests{}
	for i := 0; i < 5; i++ {
		if got := p.Pick(bs).ID; got != "b" {
			t.Fatalf("got %s, want b", got)
		}
	}
}

func TestLeastTokensPicksLowest(t *testing.T) {
	bs := testPool().All()
	bs[0].OutstandingTokens.Store(100)
	bs[1].OutstandingTokens.Store(900)
	bs[2].OutstandingTokens.Store(50)
	p := &LeastTokens{}
	for i := 0; i < 5; i++ {
		if got := p.Pick(bs).ID; got != "c" {
			t.Fatalf("got %s, want c", got)
		}
	}
}

func TestTiesAreSpread(t *testing.T) {
	bs := testPool().All()
	p := &LeastTokens{}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		seen[p.Pick(bs).ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("ties should rotate across backends, got %v", seen)
	}
}

func TestEmptyListReturnsNil(t *testing.T) {
	for _, name := range []string{"round_robin", "least_requests", "least_tokens"} {
		p, err := NewPolicy(name)
		if err != nil {
			t.Fatal(err)
		}
		if p.Pick(nil) != nil {
			t.Errorf("%s: expected nil", name)
		}
	}
	if _, err := NewPolicy("random"); err == nil {
		t.Error("expected error for unknown policy")
	}
}

func TestHealthyFiltersAndExcludes(t *testing.T) {
	pool := testPool()
	pool.All()[1].SetHealthy(false)
	got := pool.Healthy(map[string]bool{"c": true})
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %v", got)
	}
}
