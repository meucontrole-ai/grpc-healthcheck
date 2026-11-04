// Package client reads the health report of a gRPC service so a caller (usually its gateway) can nest it
// as one check of its own report, keeping the whole chain visible: gateway -> core -> core dependencies.
package client

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/meucontrole-ai/grpc-healthcheck/pkg/domain"
	"github.com/meucontrole-ai/grpc-healthcheck/pkg/pb/healthv1"
)

// DefaultTimeout bounds one remote health call.
const DefaultTimeout = 2 * time.Second

// Options configures a remote check.
type Options struct {
	// Timeout bounds the remote call; zero means DefaultTimeout.
	Timeout time.Duration
	// KeepExceptions keeps exception strings of the remote report. Leave false for public endpoints:
	// they can carry hosts, users and internal topology.
	KeepExceptions bool
}

// Probe calls HealthReport.Check on conn and returns one check result whose status is the remote
// aggregate and whose data is the remote report ({"status", "health_check"}). A service without
// HealthReport falls back to grpc.health.v1; a transport failure is Unhealthy.
func Probe(ctx context.Context, conn grpc.ClientConnInterface, opts Options) *domain.HealthCheckResult {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := healthv1.NewHealthReportClient(conn).Check(ctx, &healthv1.CheckRequest{})
	if status.Code(err) == codes.Unimplemented {
		return standard(ctx, conn, opts)
	}
	if err != nil {
		return transportFailure(err, opts)
	}
	report := FromProto(resp, opts.KeepExceptions)
	st := normalize(resp.GetStatus())
	return domain.NewResult(st, "service reported "+string(st), nil, report)
}

// NewCheck adapts Probe to the usecase.HealthCheckManager AddCheckContext signature.
func NewCheck(conn grpc.ClientConnInterface, opts Options) func(context.Context) *domain.HealthCheckResult {
	return func(ctx context.Context) *domain.HealthCheckResult { return Probe(ctx, conn, opts) }
}

// FromProto converts a wire report into the JSON report shape: {"status": ..., "health_check": [{name: result}]}.
func FromProto(resp *healthv1.CheckResponse, keepExceptions bool) map[string]any {
	checks := make([]any, 0, len(resp.GetChecks()))
	for _, c := range resp.GetChecks() {
		data := map[string]any{}
		if c.GetData() != nil {
			data = c.GetData().AsMap()
		}
		var exc any
		if keepExceptions && c.Exception != nil {
			exc = c.GetException()
		}
		if !keepExceptions {
			Redact(data)
		}
		checks = append(checks, map[string]any{c.GetName(): map[string]any{
			"status":      string(normalize(c.GetStatus())),
			"description": c.GetDescription(),
			"exception":   exc,
			"data":        data,
			"duration":    c.GetDuration(),
		}})
	}
	return map[string]any{"status": string(normalize(resp.GetStatus())), "health_check": checks}
}

// Redact clears every "exception" value found in a decoded JSON tree, in place.
func Redact(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == "exception" {
				t[k] = nil
				continue
			}
			Redact(child)
		}
	case []any:
		for _, child := range t {
			Redact(child)
		}
	}
}

func standard(ctx context.Context, conn grpc.ClientConnInterface, opts Options) *domain.HealthCheckResult {
	resp, err := healthgrpc.NewHealthClient(conn).Check(ctx, &healthgrpc.HealthCheckRequest{})
	if err != nil {
		return transportFailure(err, opts)
	}
	data := map[string]any{"grpc_health_v1": resp.GetStatus().String()}
	if resp.GetStatus() == healthgrpc.HealthCheckResponse_SERVING {
		return domain.Healthy("grpc.health.v1 SERVING (no detailed report)", data)
	}
	return domain.Unhealthy("grpc.health.v1 "+resp.GetStatus().String(), nil, data)
}

func transportFailure(err error, opts Options) *domain.HealthCheckResult {
	code := status.Code(err)
	desc := "unreachable (" + code.String() + ")"
	switch {
	case code == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded):
		desc = "timed out"
	case code == codes.Unimplemented:
		desc = "no health service registered"
	}
	var exc error
	if opts.KeepExceptions {
		exc = err
	}
	return domain.Unhealthy(desc, exc, map[string]any{"grpc_code": code.String()})
}

func normalize(s string) domain.HealthStatus {
	switch domain.HealthStatus(s) {
	case domain.HealthStatusHealthy, domain.HealthStatusDegraded:
		return domain.HealthStatus(s)
	}
	return domain.HealthStatusUnhealthy
}
