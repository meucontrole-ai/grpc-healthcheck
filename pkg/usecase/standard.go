package usecase

import (
	"context"
	"fmt"
	"regexp"

	"github.com/meucontrole-ai/grpc-healthcheck/pkg/domain"
)

// Severity says what a failed dependency does to the report: Critical makes it Unhealthy, Optional Degraded.
type Severity int

const (
	// Critical dependencies make the service Unhealthy when they fail.
	Critical Severity = iota
	// Optional dependencies (caches, fail-open paths) only make the service Degraded when they fail.
	Optional
)

// Standard descriptions written by AddPing and AddConfig, identical across every service.
const (
	DescPingOK        = "ping ok"
	DescPingFailed    = "ping failed"
	DescConfigured    = "configured"
	DescNotConfigured = "not configured"
)

// checkName is lowercase kebab words with at most one "." before a role: postgres, redis.cache, config.signing-key.
var checkName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*(\.[a-z0-9]+(-[a-z0-9]+)*)?$`)

// aliasNames are spellings of a standard name; they are rejected so every service says the same thing.
var aliasNames = map[string]string{
	"database": "postgres", "db": "postgres", "pg": "postgres", "postgresql": "postgres", "admin-db": "postgres.admin",
	"cache": "redis.cache", "mongo": "mongodb", "dynamo": "dynamodb", "self": ServingCheck,
}

// ValidateName reports why name breaks the naming standard, or nil when it follows it.
func ValidateName(name string) error {
	if name == ServingCheck {
		return fmt.Errorf("healthcheck: %q is built in; call Drain on shutdown", name)
	}
	if want, ok := aliasNames[name]; ok {
		return fmt.Errorf("healthcheck: %q is not standard, use %q", name, want)
	}
	if !checkName.MatchString(name) {
		return fmt.Errorf("healthcheck: %q must be lowercase kebab words with at most one \".\" (e.g. postgres, redis.cache)", name)
	}
	return nil
}

// AddPing registers dependency name (e.g. "postgres", "redis.cache") answered by ping with the standard descriptions.
// A ping error becomes the exception; severity decides between Unhealthy and Degraded.
func (m *HealthCheckManager) AddPing(name string, severity Severity, ping func(ctx context.Context) error) {
	m.AddCheckContext(name, func(ctx context.Context) *domain.HealthCheckResult {
		if err := ping(ctx); err != nil {
			return failed(severity, DescPingFailed, err)
		}
		return domain.Healthy(DescPingOK, nil)
	})
}

// AddConfig registers local configuration name (e.g. "config.signing-key") that is Healthy while configured reports true.
func (m *HealthCheckManager) AddConfig(name string, severity Severity, configured func() bool) {
	m.AddCheckFunc(name, func() *domain.HealthCheckResult {
		if !configured() {
			return failed(severity, DescNotConfigured, nil)
		}
		return domain.Healthy(DescConfigured, nil)
	})
}

func failed(severity Severity, desc string, err error) *domain.HealthCheckResult {
	if severity == Optional {
		return domain.Degraded(desc, err, nil)
	}
	return domain.Unhealthy(desc, err, nil)
}
