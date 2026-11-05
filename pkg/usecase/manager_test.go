package usecase

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meucontrole-ai/grpc-healthcheck/pkg/domain"
)

func noCache() HealthCheckOptions {
	return HealthCheckOptions{CacheTTL: -1, Timeout: 100 * time.Millisecond}
}

func TestAggregation(t *testing.T) {
	cases := []struct {
		name   string
		checks []domain.HealthStatus
		code   int
		status domain.HealthStatus
	}{
		{"no checks is healthy", nil, 200, domain.HealthStatusHealthy},
		{"all healthy", []domain.HealthStatus{"Healthy", "Healthy"}, 200, domain.HealthStatusHealthy},
		{"one unhealthy among healthy is unhealthy", []domain.HealthStatus{"Healthy", "Unhealthy"}, 503, domain.HealthStatusUnhealthy},
		{"all degraded is degraded", []domain.HealthStatus{"Degraded", "Degraded"}, 200, domain.HealthStatusDegraded},
		{"degraded and unhealthy is unhealthy", []domain.HealthStatus{"Degraded", "Unhealthy"}, 503, domain.HealthStatusUnhealthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewHealthCheckManager(noCache())
			for i, s := range tc.checks {
				s := s
				m.AddCheckFunc(string(rune('a'+i)), func() *domain.HealthCheckResult { return domain.NewResult(s, "x", nil, nil) })
			}
			code, r := m.Run(context.Background())
			if code != tc.code || r.Status != tc.status {
				t.Fatalf("got %d %s, want %d %s", code, r.Status, tc.code, tc.status)
			}
		})
	}
}

func TestTimeoutPanicNilAndInvalid(t *testing.T) {
	m := NewHealthCheckManager(noCache())
	m.AddCheckContext("slow", func(ctx context.Context) *domain.HealthCheckResult {
		<-ctx.Done()
		time.Sleep(time.Second)
		return domain.Healthy("late", nil)
	})
	m.AddCheckFunc("panic", func() *domain.HealthCheckResult { panic("boom") })
	m.AddCheckFunc("nil", func() *domain.HealthCheckResult { return nil })
	m.AddCheckFunc("bogus", func() *domain.HealthCheckResult { return domain.NewResult("Weird", "x", nil, nil) })
	start := time.Now()
	code, r := m.Run(context.Background())
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("timeout not enforced")
	}
	if code != 503 {
		t.Fatalf("code %d", code)
	}
	names := []string{"bogus", "nil", "panic", ServingCheck, "slow"}
	for i, entry := range r.HealthCheck {
		res, ok := entry[names[i]]
		if !ok {
			t.Fatalf("order: entry %d is %v", i, entry)
		}
		want := domain.HealthStatusUnhealthy
		if names[i] == ServingCheck {
			want = domain.HealthStatusHealthy
		}
		if res.Status != want {
			t.Fatalf("%s: %s", names[i], res.Status)
		}
	}
}

func TestServingIsBuiltInAndDrains(t *testing.T) {
	m := NewHealthCheckManager()
	m.AddCheckFunc("postgres", func() *domain.HealthCheckResult { return domain.Healthy("ok", nil) })

	code, r := m.Run(context.Background())
	if code != 200 || r.Status != domain.HealthStatusHealthy || len(r.HealthCheck) != 2 {
		t.Fatalf("before drain: %d %s %v", code, r.Status, r.HealthCheck)
	}

	m.Drain()
	code, r = m.Run(context.Background())
	if code != 503 || r.Status != domain.HealthStatusUnhealthy || !m.Draining() {
		t.Fatalf("after drain: %d %s", code, r.Status)
	}
	if r.HealthCheck[1][ServingCheck].Status != domain.HealthStatusUnhealthy {
		t.Fatalf("serving not drained: %v", r.HealthCheck)
	}
}

func TestReservedNamesPanic(t *testing.T) {
	for _, name := range []string{ServingCheck, "self"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%q accepted", name)
				}
			}()
			NewHealthCheckManager().AddCheckFunc(name, func() *domain.HealthCheckResult { return domain.Healthy("x", nil) })
		}()
	}
}

func TestDetailsHiddenByDefault(t *testing.T) {
	var logged string
	m := NewHealthCheckManager(HealthCheckOptions{CacheTTL: -1, OnResult: func(_ string, r domain.HealthCheckResult) {
		if r.Exception != nil {
			logged = *r.Exception
		}
	}})
	m.AddCheckFunc("postgres", func() *domain.HealthCheckResult {
		return domain.Unhealthy("db down", errors.New("dial postgres://app:secret@10.0.0.5"), map[string]string{"host": "10.0.0.5"})
	})
	_, r := m.Run(context.Background())
	res := r.HealthCheck[0]["postgres"]
	if res.Exception != nil || len(res.Data.(map[string]any)) != 0 {
		t.Fatalf("details leaked: %+v", res)
	}
	if logged == "" {
		t.Fatal("OnResult must receive full details")
	}
	m2 := NewHealthCheckManager(HealthCheckOptions{CacheTTL: -1, ExposeDetails: true})
	m2.AddCheckFunc("postgres", func() *domain.HealthCheckResult { return domain.Unhealthy("db down", errors.New("x"), nil) })
	_, r2 := m2.Run(context.Background())
	if r2.HealthCheck[0]["postgres"].Exception == nil {
		t.Fatal("ExposeDetails must expose exception")
	}
}

func TestCacheAndSingleFlight(t *testing.T) {
	var calls atomic.Int32
	m := NewHealthCheckManager(HealthCheckOptions{CacheTTL: time.Minute})
	m.AddCheckFunc("postgres", func() *domain.HealthCheckResult {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return domain.Healthy("ok", nil)
	})
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() { m.Run(context.Background()); done <- struct{}{} }()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("check ran %d times, want 1", n)
	}
}
