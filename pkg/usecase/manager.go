// Package usecase runs the registered health checks and aggregates their results.
package usecase

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/meucontrole-ai/grpc-healthcheck/pkg/domain"
)

// HealthCheckOptions configures the manager. Zero values fall back to DefaultOptions.
type HealthCheckOptions struct {
	StatusCodeHealthy   int
	StatusCodeDegraded  int
	StatusCodeUnhealthy int
	// Timeout bounds every single check. A check that does not answer in time is Unhealthy.
	Timeout time.Duration
	// CacheTTL reuses the last aggregated result for this long, so probe floods never
	// multiply load on dependencies. Concurrent callers share one in-flight run.
	// A negative value disables caching.
	CacheTTL time.Duration
	// ExposeDetails includes exception messages and data in responses. Keep it false on
	// public listeners: error strings can leak hosts, users and internal topology.
	ExposeDetails bool
	// OnResult, when set, receives every check result with full details (use it to log failures).
	OnResult func(name string, result domain.HealthCheckResult)
}

// DefaultOptions returns the default configuration.
func DefaultOptions() HealthCheckOptions {
	return HealthCheckOptions{
		StatusCodeHealthy:   200,
		StatusCodeDegraded:  200,
		StatusCodeUnhealthy: 503,
		Timeout:             3 * time.Second,
		CacheTTL:            time.Second,
	}
}

// Report is the aggregated answer of a run.
type Report struct {
	Status      domain.HealthStatus                    `json:"status"`
	HealthCheck []map[string]*domain.HealthCheckResult `json:"health_check"`
}

type namedCheck struct {
	name  string
	check domain.ContextHealthCheck
}

// ServingCheck is the built-in check of every manager: Healthy until Drain, Unhealthy afterwards.
const ServingCheck = "serving"

// reservedChecks cannot be registered: "serving" is built in and "self" would only restate it.
var reservedChecks = map[string]bool{ServingCheck: true, "self": true}

// HealthCheckManager registers and runs health checks. It is safe for concurrent use.
type HealthCheckManager struct {
	mu       sync.RWMutex
	checks   map[string]domain.ContextHealthCheck
	options  HealthCheckOptions
	draining atomic.Bool

	runMu    sync.Mutex
	cached   *cachedRun
	inFlight chan struct{}
}

type cachedRun struct {
	code   int
	report Report
	at     time.Time
}

// NewHealthCheckManager creates a manager; an optional HealthCheckOptions overrides the defaults.
func NewHealthCheckManager(opts ...HealthCheckOptions) *HealthCheckManager {
	options := DefaultOptions()
	if len(opts) > 0 {
		o := opts[0]
		if o.StatusCodeHealthy != 0 {
			options.StatusCodeHealthy = o.StatusCodeHealthy
		}
		if o.StatusCodeDegraded != 0 {
			options.StatusCodeDegraded = o.StatusCodeDegraded
		}
		if o.StatusCodeUnhealthy != 0 {
			options.StatusCodeUnhealthy = o.StatusCodeUnhealthy
		}
		if o.Timeout > 0 {
			options.Timeout = o.Timeout
		}
		if o.CacheTTL != 0 {
			options.CacheTTL = o.CacheTTL
		}
		options.ExposeDetails = o.ExposeDetails
		options.OnResult = o.OnResult
	}
	m := &HealthCheckManager{checks: make(map[string]domain.ContextHealthCheck), options: options}
	m.checks[ServingCheck] = funcCheck(func(context.Context) *domain.HealthCheckResult {
		if m.draining.Load() {
			return domain.Unhealthy("shutting down", nil, nil)
		}
		return domain.Healthy("serving", nil)
	})
	return m
}

// Drain flips the built-in "serving" check to Unhealthy; call it when shutdown starts, before
// the server stops accepting work, so probes and callers route away first.
func (m *HealthCheckManager) Drain() {
	m.draining.Store(true)
	m.runMu.Lock()
	m.cached = nil
	m.runMu.Unlock()
}

// Draining reports whether Drain was called.
func (m *HealthCheckManager) Draining() bool {
	return m.draining.Load()
}

type legacyCheck struct{ c domain.HealthCheck }

func (l legacyCheck) CheckHealthContext(context.Context) *domain.HealthCheckResult {
	return l.c.CheckHealth()
}

type funcCheck func(context.Context) *domain.HealthCheckResult

func (f funcCheck) CheckHealthContext(ctx context.Context) *domain.HealthCheckResult { return f(ctx) }

// AddCheck registers a check; a check with the same name is replaced.
// A check that also implements ContextHealthCheck receives the request context.
func (m *HealthCheckManager) AddCheck(name string, check domain.HealthCheck) {
	if cc, ok := check.(domain.ContextHealthCheck); ok {
		m.add(name, cc)
		return
	}
	m.add(name, legacyCheck{check})
}

// AddCheckFunc registers a function as a check.
func (m *HealthCheckManager) AddCheckFunc(name string, f func() *domain.HealthCheckResult) {
	m.add(name, funcCheck(func(context.Context) *domain.HealthCheckResult { return f() }))
}

// AddCheckContext registers a context-aware function as a check.
func (m *HealthCheckManager) AddCheckContext(name string, f func(ctx context.Context) *domain.HealthCheckResult) {
	m.add(name, funcCheck(f))
}

func (m *HealthCheckManager) add(name string, c domain.ContextHealthCheck) {
	if reservedChecks[name] {
		panic(fmt.Sprintf("healthcheck: %q is reserved (serving is built in; use Drain on shutdown)", name))
	}
	m.mu.Lock()
	m.checks[name] = c
	m.mu.Unlock()
	m.runMu.Lock()
	m.cached = nil
	m.runMu.Unlock()
}

// GetResult runs every check and returns the HTTP status code and the response body.
// Kept for compatibility; prefer Run.
func (m *HealthCheckManager) GetResult() (int, map[string]any) {
	code, r := m.Run(context.Background())
	return code, map[string]any{"status": r.Status, "health_check": r.HealthCheck}
}

// Run executes all checks concurrently (each bounded by Timeout) and aggregates them.
// Status: any Unhealthy -> Unhealthy; else any Degraded -> Degraded; else Healthy.
func (m *HealthCheckManager) Run(ctx context.Context) (int, Report) {
	if m.options.CacheTTL < 0 {
		return m.run(ctx)
	}
	for {
		m.runMu.Lock()
		if c := m.cached; c != nil && time.Since(c.at) < m.options.CacheTTL {
			m.runMu.Unlock()
			return c.code, c.report
		}
		if wait := m.inFlight; wait != nil {
			m.runMu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return m.options.StatusCodeUnhealthy, Report{Status: domain.HealthStatusUnhealthy, HealthCheck: []map[string]*domain.HealthCheckResult{}}
			}
		}
		done := make(chan struct{})
		m.inFlight = done
		m.runMu.Unlock()

		// Detach from the caller so one cancelled probe does not poison the shared result.
		code, report := m.run(context.WithoutCancel(ctx))

		m.runMu.Lock()
		m.cached = &cachedRun{code: code, report: report, at: time.Now()}
		m.inFlight = nil
		m.runMu.Unlock()
		close(done)
		return code, report
	}
}

func (m *HealthCheckManager) run(ctx context.Context) (int, Report) {
	m.mu.RLock()
	list := make([]namedCheck, 0, len(m.checks))
	for n, c := range m.checks {
		list = append(list, namedCheck{n, c})
	}
	m.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })

	results := make([]*domain.HealthCheckResult, len(list))
	var wg sync.WaitGroup
	for i, nc := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = m.runOne(ctx, nc.check)
		}()
	}
	wg.Wait()

	status := domain.HealthStatusHealthy
	out := make([]map[string]*domain.HealthCheckResult, 0, len(list))
	for i, nc := range list {
		r := results[i]
		if m.options.OnResult != nil {
			m.options.OnResult(nc.name, *r)
		}
		switch r.Status {
		case domain.HealthStatusUnhealthy:
			status = domain.HealthStatusUnhealthy
		case domain.HealthStatusDegraded:
			if status == domain.HealthStatusHealthy {
				status = domain.HealthStatusDegraded
			}
		}
		if !m.options.ExposeDetails {
			pub := *r
			pub.Exception = nil
			pub.Data = map[string]any{}
			r = &pub
		}
		out = append(out, map[string]*domain.HealthCheckResult{nc.name: r})
	}
	return m.statusCode(status), Report{Status: status, HealthCheck: out}
}

func (m *HealthCheckManager) runOne(parent context.Context, c domain.ContextHealthCheck) *domain.HealthCheckResult {
	ctx, cancel := context.WithTimeout(parent, m.options.Timeout)
	defer cancel()
	start := time.Now()
	ch := make(chan *domain.HealthCheckResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- domain.Unhealthy("check panicked", fmt.Errorf("panic: %v", r), nil)
			}
		}()
		ch <- c.CheckHealthContext(ctx)
	}()
	var res *domain.HealthCheckResult
	select {
	case res = <-ch:
		if res == nil {
			res = domain.Unhealthy("check returned no result", nil, nil)
		}
		cp := *res
		res = &cp
	case <-ctx.Done():
		res = domain.Unhealthy("check timed out", ctx.Err(), nil)
	}
	if res.Data == nil {
		res.Data = map[string]any{}
	}
	switch res.Status {
	case domain.HealthStatusHealthy, domain.HealthStatusDegraded, domain.HealthStatusUnhealthy:
	default:
		res.Status = domain.HealthStatusUnhealthy
	}
	res.Duration = formatDuration(time.Since(start))
	return res
}

func (m *HealthCheckManager) statusCode(s domain.HealthStatus) int {
	switch s {
	case domain.HealthStatusUnhealthy:
		return m.options.StatusCodeUnhealthy
	case domain.HealthStatusDegraded:
		return m.options.StatusCodeDegraded
	}
	return m.options.StatusCodeHealthy
}

func formatDuration(d time.Duration) string {
	h := d / time.Hour
	d -= h * time.Hour
	mi := d / time.Minute
	d -= mi * time.Minute
	s := d / time.Second
	d -= s * time.Second
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, mi, s, d/time.Millisecond)
}
