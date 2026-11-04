# grpc-healthcheck

The meucontrole health contract for gRPC services. Same `domain` and `usecase` as
[fiber-healthcheck](https://github.com/meucontrole-ai/fiber-healthcheck) (checks, timeouts, cache,
single flight, `Healthy`/`Degraded`/`Unhealthy` aggregation), with no HTTP framework: a gRPC core
answers its health over gRPC only.

## Server (gRPC core)

```go
hc := usecase.NewHealthCheckManager(usecase.HealthCheckOptions{ExposeDetails: true})
hc.AddCheckContext("postgres", func(ctx context.Context) *domain.HealthCheckResult {
	if err := sqlDB.PingContext(ctx); err != nil {
		return domain.Unhealthy("ping failed", err, nil)
	}
	return domain.Healthy("ping ok", nil)
})

grpc_adapter.Register(grpcServer, hc)
```

`Register` mounts two services answered by the same manager:

| Service | Use |
| --- | --- |
| `meucontrole.health.v1.HealthReport/Check` | full report: every check with status, description, exception, data, duration |
| `grpc.health.v1.Health` | standard probe (k8s `grpc:` probe, grpcurl): `NOT_SERVING` when the report is `Unhealthy` |

`ExposeDetails: true` is expected: the report only travels inside the cluster and the caller decides
what to publish.

## Client (gateway)

```go
hc.AddCheckContext("merchant-service-core", client.NewCheck(conn, client.Options{Timeout: 2 * time.Second}))
```

`client.Probe` returns one check whose status is the remote aggregate and whose `data` is the remote
report (`{"status", "health_check"}`), so the gateway's JSON shows the whole chain:
gateway -> core -> core dependencies. A service without `HealthReport` falls back to
`grpc.health.v1`; a transport failure is `Unhealthy`. Exceptions of the remote report are cleared
unless `KeepExceptions` is set (they can carry hosts and users); `client.Redact` clears them from any
decoded JSON tree.

## Regenerate

```bash
protoc -I proto --go_out=. --go_opt=module=github.com/meucontrole-ai/grpc-healthcheck \
  --go-grpc_out=. --go-grpc_opt=module=github.com/meucontrole-ai/grpc-healthcheck \
  proto/meucontrole/health/v1/health.proto
```
