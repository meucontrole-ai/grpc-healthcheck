// Package grpc_adapter exposes the health check manager on a gRPC server: the detailed
// meucontrole.health.v1.HealthReport service and the standard grpc.health.v1.Health service.
package grpc_adapter

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/meucontrole-ai/grpc-healthcheck/pkg/domain"
	"github.com/meucontrole-ai/grpc-healthcheck/pkg/pb/healthv1"
	"github.com/meucontrole-ai/grpc-healthcheck/pkg/usecase"
)

// DefaultWatchInterval is how often grpc.health.v1 Watch re-runs the checks to detect a change.
const DefaultWatchInterval = 10 * time.Second

// Options configures the gRPC health services.
type Options struct {
	// WatchInterval paces grpc.health.v1 Watch; zero means DefaultWatchInterval.
	WatchInterval time.Duration
}

// Register mounts HealthReport and grpc.health.v1.Health, both answered by m, on s.
// Build m with ExposeDetails true: the report only travels inside the cluster and callers strip it.
func Register(s grpc.ServiceRegistrar, m *usecase.HealthCheckManager, opts ...Options) {
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.WatchInterval <= 0 {
		o.WatchInterval = DefaultWatchInterval
	}
	healthv1.RegisterHealthReportServer(s, &reportServer{m: m})
	healthgrpc.RegisterHealthServer(s, &standardServer{m: m, watchEvery: o.WatchInterval})
}

type reportServer struct {
	healthv1.UnimplementedHealthReportServer
	m *usecase.HealthCheckManager
}

func (r *reportServer) Check(ctx context.Context, _ *healthv1.CheckRequest) (*healthv1.CheckResponse, error) {
	_, report := r.m.Run(ctx)
	return ToProto(report), nil
}

// ToProto converts a manager report into the wire response.
func ToProto(report usecase.Report) *healthv1.CheckResponse {
	out := &healthv1.CheckResponse{Status: string(report.Status), Checks: make([]*healthv1.CheckResult, 0, len(report.HealthCheck))}
	for _, entry := range report.HealthCheck {
		for name, res := range entry {
			out.Checks = append(out.Checks, &healthv1.CheckResult{
				Name:        name,
				Status:      string(res.Status),
				Description: res.Description,
				Exception:   res.Exception,
				Data:        toStruct(res.Data),
				Duration:    res.Duration,
			})
		}
	}
	return out
}

func toStruct(data any) *structpb.Struct {
	m := domain.DataMap(data)
	if len(m) == 0 {
		return nil
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil
	}
	return s
}

type standardServer struct {
	healthgrpc.UnimplementedHealthServer
	m          *usecase.HealthCheckManager
	watchEvery time.Duration
}

func (h *standardServer) serving(ctx context.Context) healthgrpc.HealthCheckResponse_ServingStatus {
	_, report := h.m.Run(ctx)
	if report.Status == domain.HealthStatusUnhealthy {
		return healthgrpc.HealthCheckResponse_NOT_SERVING
	}
	return healthgrpc.HealthCheckResponse_SERVING
}

func (h *standardServer) Check(ctx context.Context, _ *healthgrpc.HealthCheckRequest) (*healthgrpc.HealthCheckResponse, error) {
	return &healthgrpc.HealthCheckResponse{Status: h.serving(ctx)}, nil
}

func (h *standardServer) Watch(_ *healthgrpc.HealthCheckRequest, stream healthgrpc.Health_WatchServer) error {
	ctx := stream.Context()
	last := healthgrpc.HealthCheckResponse_UNKNOWN
	ticker := time.NewTicker(h.watchEvery)
	defer ticker.Stop()
	for {
		if cur := h.serving(ctx); cur != last {
			if err := stream.Send(&healthgrpc.HealthCheckResponse{Status: cur}); err != nil {
				return err
			}
			last = cur
		}
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-ticker.C:
		}
	}
}

func (h *standardServer) List(ctx context.Context, _ *healthgrpc.HealthListRequest) (*healthgrpc.HealthListResponse, error) {
	if ctx.Err() != nil {
		return nil, status.Error(codes.Canceled, "cancelled")
	}
	return &healthgrpc.HealthListResponse{Statuses: map[string]*healthgrpc.HealthCheckResponse{"": {Status: h.serving(ctx)}}}, nil
}
