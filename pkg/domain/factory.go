package domain

// NewResult creates a new HealthCheckResult with the specified parameters.
//
// Parameters:
//   - status: The health status (Healthy, Degraded, Unhealthy).
//   - description: A human-readable description of the check result.
//   - exception: An error object if the check failed or is degraded (can be nil).
//   - data: Additional context or metrics (can be nil).
//
// Returns:
//   - *HealthCheckResult: A pointer to the created result struct.
func NewResult(status HealthStatus, description string, exception error, data any) *HealthCheckResult {
	var excStr *string
	if exception != nil {
		s := exception.Error()
		excStr = &s
	}

	// Ensure data is not nil for JSON empty object
	if data == nil {
		data = map[string]any{}
	}

	return &HealthCheckResult{
		Status:      status,
		Description: description,
		Exception:   excStr,
		Data:        data,
	}
}

// Healthy creates a HealthCheckResult with a Healthy status.
//
// Parameters:
//   - description: A human-readable description of the check result.
//   - data: Additional context or metrics (can be nil).
func Healthy(description string, data any) *HealthCheckResult {
	return NewResult(HealthStatusHealthy, description, nil, data)
}

// Degraded creates a HealthCheckResult with a Degraded status.
//
// Parameters:
//   - description: A human-readable description of the check result.
//   - exception: An error object explaining why the status is degraded (can be nil).
//   - data: Additional context or metrics (can be nil).
func Degraded(description string, exception error, data any) *HealthCheckResult {
	return NewResult(HealthStatusDegraded, description, exception, data)
}

// Unhealthy creates a HealthCheckResult with an Unhealthy status.
//
// Parameters:
//   - description: A human-readable description of the check result.
//   - exception: An error object explaining why the status is unhealthy (can be nil).
//   - data: Additional context or metrics (can be nil).
func Unhealthy(description string, exception error, data any) *HealthCheckResult {
	return NewResult(HealthStatusUnhealthy, description, exception, data)
}
