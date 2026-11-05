# Architecture

`grpc-healthcheck` is a Go library (no binary, no deployment) that serves the MeuControle health contract over gRPC only. The `domain` and `usecase` packages are the same as in [fiber-healthcheck](https://github.com/meucontrole-ai/fiber-healthcheck); this repo replaces the HTTP adapters with a gRPC server adapter and a gRPC client.

## Layers

| Package | Role |
| --- | --- |
| `pkg/domain` | Contract types: `HealthStatus`, `HealthCheckResult`, the check interfaces and the `Healthy` / `Degraded` / `Unhealthy` factories. No dependencies. |
| `pkg/usecase` | `HealthCheckManager`: registration, concurrent runs with a per-check timeout, result cache with single flight, aggregation, the built-in `serving` check with `Drain()`, and the naming standard (`AddPing`, `AddConfig`). |
| `pkg/adapter/grpc` | `Register(server, manager, Options)` mounts `meucontrole.health.v1.HealthReport` (full report) and `grpc.health.v1.Health` (liveness: `Check`, `Watch`, `List`). `ToProto` converts a `usecase.Report`. |
| `pkg/client` | Gateway side: `Probe` / `NewCheck` call a core's `HealthReport` (falling back to `grpc.health.v1`) and return one check carrying the remote report; `FromProto` and `Redact` decode and scrub it. |
| `pkg/pb/healthv1` | Generated Go code for `proto/meucontrole/health/v1/health.proto`. |

## Two services, one manager

- `HealthReport/Check` runs every check and returns the aggregated report (status, description, exception, data, duration per check). Gateways and operators read it.
- `grpc.health.v1.Health` answers `SERVING` / `NOT_SERVING` from the liveness checks only (`serving` by default, configurable with `Options.LivenessChecks`). Orchestrator probes (`grpc_health_probe` in ECS, `grpc:` probes in k8s) point here, so a dependency outage never restarts every task.

## Health chain

A gateway registers each upstream core as a check named after the service (`client.NewCheck(conn, ...)`). Its own `/health` (served with fiber-healthcheck) then embeds the core's report as `data`, giving gateway → core → dependencies in one answer. Remote exceptions are cleared unless `KeepExceptions` is set.
