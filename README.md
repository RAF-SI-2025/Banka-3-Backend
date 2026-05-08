# Banka-3-Backend

Go monorepo. Six services (`user`, `bank`, `trading`, `exchange`,
`notification`, `gateway`) talking gRPC, Postgres + Redis, deployed on
Kubernetes.

See `CLAUDE.md` for architecture, conventions, and roadmap. Quick start:

```bash
cp .env.example .env
make proto
make up
make migrate
```

The gateway listens on `GATEWAY_HTTP_PORT` (default `8080`); each service
exposes its gRPC port plus an HTTP probe port (`/healthz`, `/readyz`).
