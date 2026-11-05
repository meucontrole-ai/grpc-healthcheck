package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/meucontrole-ai/grpc-healthcheck/pkg/domain"
)

func TestAddPingAndAddConfigUseStandardDescriptions(t *testing.T) {
	m := NewHealthCheckManager(HealthCheckOptions{CacheTTL: -1, ExposeDetails: true})
	m.AddPing("postgres", Critical, func(context.Context) error { return errors.New("dial refused") })
	m.AddPing("redis.cache", Optional, func(context.Context) error { return errors.New("timeout") })
	m.AddPing("dynamodb", Critical, func(context.Context) error { return nil })
	m.AddConfig("config.signing-key", Optional, func() bool { return false })

	_, r := m.Run(context.Background())
	got := map[string]*domain.HealthCheckResult{}
	for _, e := range r.HealthCheck {
		for n, v := range e {
			got[n] = v
		}
	}
	want := map[string][2]string{
		"postgres":           {string(domain.HealthStatusUnhealthy), DescPingFailed},
		"redis.cache":        {string(domain.HealthStatusDegraded), DescPingFailed},
		"dynamodb":           {string(domain.HealthStatusHealthy), DescPingOK},
		"config.signing-key": {string(domain.HealthStatusDegraded), DescNotConfigured},
		ServingCheck:         {string(domain.HealthStatusHealthy), "serving"},
	}
	for n, w := range want {
		if got[n] == nil || string(got[n].Status) != w[0] || got[n].Description != w[1] {
			t.Fatalf("%s = %+v, want %v", n, got[n], w)
		}
	}
	if got["postgres"].Exception == nil || *got["postgres"].Exception != "dial refused" {
		t.Fatalf("ping error not kept as exception: %+v", got["postgres"])
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"postgres", "postgres.admin", "redis.cache", "sqs.consumer", "hydra.admin", "merchant-service-core", "config.pos-device-issuer-key"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"serving", "self", "database", "db", "admin-db", "cache", "admin_db", "redis_cache", "Postgres", "a.b.c", "", "-x"} {
		if ValidateName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
