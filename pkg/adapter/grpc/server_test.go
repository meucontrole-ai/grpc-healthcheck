package grpc_adapter_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"

	grpc_adapter "github.com/meucontrole-ai/grpc-healthcheck/pkg/adapter/grpc"
	"github.com/meucontrole-ai/grpc-healthcheck/pkg/client"
	"github.com/meucontrole-ai/grpc-healthcheck/pkg/domain"
	"github.com/meucontrole-ai/grpc-healthcheck/pkg/usecase"
)

func dial(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func manager(dbErr error) *usecase.HealthCheckManager {
	m := usecase.NewHealthCheckManager(usecase.HealthCheckOptions{CacheTTL: -1, ExposeDetails: true})
	m.AddCheckFunc("postgres", func() *domain.HealthCheckResult {
		if dbErr != nil {
			return domain.Unhealthy("ping failed", dbErr, map[string]any{"pool": "admin"})
		}
		return domain.Healthy("ping ok", map[string]any{"pool": "admin"})
	})
	m.AddCheckFunc("redis", func() *domain.HealthCheckResult { return domain.Degraded("slow", nil, nil) })
	return m
}

func TestProbeNestsRemoteReport(t *testing.T) {
	conn := dial(t, func(s *grpc.Server) { grpc_adapter.Register(s, manager(errors.New("dial tcp 10.0.0.1:5432: refused"))) })

	res := client.Probe(context.Background(), conn, client.Options{})

	if res.Status != domain.HealthStatusUnhealthy {
		t.Fatalf("status = %s, want Unhealthy", res.Status)
	}
	report := res.Data.(map[string]any)
	checks := report["health_check"].([]any)
	if len(checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(checks))
	}
	pg := checks[0].(map[string]any)["postgres"].(map[string]any)
	if pg["status"] != "Unhealthy" || pg["exception"] != nil || pg["data"].(map[string]any)["pool"] != "admin" {
		t.Fatalf("postgres = %#v", pg)
	}
}

func TestProbeKeepsExceptionsWhenAsked(t *testing.T) {
	conn := dial(t, func(s *grpc.Server) { grpc_adapter.Register(s, manager(errors.New("boom"))) })

	res := client.Probe(context.Background(), conn, client.Options{KeepExceptions: true})

	pg := res.Data.(map[string]any)["health_check"].([]any)[0].(map[string]any)["postgres"].(map[string]any)
	if pg["exception"] != "boom" {
		t.Fatalf("exception = %v, want boom", pg["exception"])
	}
}

func TestStandardHealthFollowsReport(t *testing.T) {
	ok := dial(t, func(s *grpc.Server) { grpc_adapter.Register(s, manager(nil)) })
	bad := dial(t, func(s *grpc.Server) { grpc_adapter.Register(s, manager(errors.New("x"))) })

	for conn, want := range map[*grpc.ClientConn]healthgrpc.HealthCheckResponse_ServingStatus{
		ok: healthgrpc.HealthCheckResponse_SERVING, bad: healthgrpc.HealthCheckResponse_NOT_SERVING,
	} {
		resp, err := healthgrpc.NewHealthClient(conn).Check(context.Background(), &healthgrpc.HealthCheckRequest{})
		if err != nil || resp.GetStatus() != want {
			t.Fatalf("status = %v err = %v, want %v", resp.GetStatus(), err, want)
		}
	}
}

func TestProbeFallsBackToStandardHealth(t *testing.T) {
	conn := dial(t, func(s *grpc.Server) {
		hs := &servingOnly{}
		healthgrpc.RegisterHealthServer(s, hs)
	})

	res := client.Probe(context.Background(), conn, client.Options{})

	if res.Status != domain.HealthStatusHealthy {
		t.Fatalf("status = %s, want Healthy", res.Status)
	}
}

func TestProbeReportsUnreachable(t *testing.T) {
	conn := dial(t, func(*grpc.Server) {})

	res := client.Probe(context.Background(), conn, client.Options{})

	if res.Status != domain.HealthStatusUnhealthy || res.Exception != nil {
		t.Fatalf("res = %#v", res)
	}
}

type servingOnly struct {
	healthgrpc.UnimplementedHealthServer
}

func (servingOnly) Check(context.Context, *healthgrpc.HealthCheckRequest) (*healthgrpc.HealthCheckResponse, error) {
	return &healthgrpc.HealthCheckResponse{Status: healthgrpc.HealthCheckResponse_SERVING}, nil
}
