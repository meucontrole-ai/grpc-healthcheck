# Development and release

## Run locally

```bash
go vet ./...
go test -race -count=1 ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

## Regenerate the protobuf code

After editing `proto/meucontrole/health/v1/health.proto`, run the `protoc` command from the README; it writes `pkg/pb/healthv1`. Commit the generated files with the proto change. The proto is a published contract: add fields, never renumber or remove them.

## CI

`.github/workflows/ci.yml` runs vet, race tests and govulncheck on the latest Go 1.26 patch on pushes to `main` and on pull requests.

## Dependency updates

`.github/dependabot.yml` opens weekly (Monday 06:00 America/Sao_Paulo) grouped minor/patch updates for Go modules and GitHub Actions against `main`.

## Release

Consumers pin semver tags (`v0.1.0` … `v0.4.0`; the platform standard requires v0.4+). To release, merge to `main` with CI green and push an annotated tag `vX.Y.Z`. Keep `domain` and `usecase` in step with fiber-healthcheck: a change there ships in both libraries with matching behaviour.

## Adopting in a core

1. `grpc_adapter.Register(grpcServer, hc)` next to the service registrations.
2. Register dependency checks with `AddPing` and the standard names.
3. Call `hc.Drain()` right before `GracefulStop()`.
4. Container: `grpc_health_probe` in the Dockerfile `HEALTHCHECK`, readiness probe `grpc`, liveness probe `tcpSocket`. No HTTP health endpoint on a pure gRPC core.
