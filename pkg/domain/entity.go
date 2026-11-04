// Package domain defines the core entities and interfaces for the health check system.
package domain

import "context"

// HealthStatus represents the status of a health check: Healthy, Degraded or Unhealthy.
type HealthStatus string

const (
	// HealthStatusHealthy indicates that the component is working correctly.
	HealthStatusHealthy HealthStatus = "Healthy"
	// HealthStatusDegraded indicates that the component works with issues (e.g. high latency).
	HealthStatusDegraded HealthStatus = "Degraded"
	// HealthStatusUnhealthy indicates that the component is not working or is unreachable.
	HealthStatusUnhealthy HealthStatus = "Unhealthy"
)

// HealthCheckResult is the result of a single health check execution.
type HealthCheckResult struct {
	Status      HealthStatus `json:"status"`
	Description string       `json:"description"`
	// Exception is the error message when the check failed or is degraded.
	// It is only exposed when the manager runs with ExposeDetails.
	Exception *string `json:"exception"`
	// Data holds extra context. It is only exposed when the manager runs with ExposeDetails.
	Data     any    `json:"data"`
	Duration string `json:"duration"`
}

// HealthCheck is implemented by components that provide a health check.
type HealthCheck interface {
	CheckHealth() *HealthCheckResult
}

// ContextHealthCheck is a HealthCheck that honours cancellation and deadlines.
// Prefer it for any check that does I/O (database, cache, gRPC, HTTP).
type ContextHealthCheck interface {
	CheckHealthContext(ctx context.Context) *HealthCheckResult
}
