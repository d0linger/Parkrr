# Parkrr monitoring (Prometheus + Grafana)

Parkrr already exports Prometheus metrics at `/metrics`. This folder adds a
drop-in monitoring stack: Prometheus to scrape them and Grafana with a
pre-provisioned datasource and the **Parkrr — Service Overview** dashboard.

## Metrics exposed by the app

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `parkrr_http_requests_total` | counter | `route`, `method`, `status` | requests, by matched route pattern (path IDs collapsed to `{id}`) |
| `parkrr_http_request_duration_seconds` | histogram | `route`, `method` | request latency (`_bucket`/`_sum`/`_count`) |
| `parkrr_db_pool_acquired_conns` | gauge | – | currently acquired DB connections |

## Run it

From the repository root, layer the monitoring compose file on top of the main one:

```sh
docker compose -f docker-compose.yml \
               -f deploy/monitoring/docker-compose.monitoring.yml up -d
```

- Grafana: http://localhost:3000 (`admin` / `admin` on first login — change it).
  The dashboard is under the **Parkrr** folder.
- Prometheus: http://localhost:9090

Both services join the app's `parkrr-net` network, so Prometheus scrapes the app
at `app:8080/metrics` — no host port required.

Override any of these before `up` if the defaults clash:
`PARKRR_GRAFANA_PORT` (3000), `PARKRR_PROMETHEUS_PORT` (9090),
`GRAFANA_ADMIN_USER`, `GRAFANA_ADMIN_PASSWORD`.

## Securing /metrics

By default `/metrics` is served without authentication — fine on a private
network. To require a token:

1. Set `PARKRR_METRICS_TOKEN=<secret>` on the app (see `docker-compose.yml`).
2. Write the same secret (one line, no trailing newline) to
   `deploy/monitoring/metrics_token`.
3. Uncomment the `authorization:` block in `prometheus.yml` and the matching
   volume mount in `docker-compose.monitoring.yml`, then recreate the stack.

To disable the endpoint entirely, set `PARKRR_METRICS_REQUIRE_AUTH=true` with an
empty token (the app fails closed and does not register `/metrics`).

## Importing the dashboard manually

If you run Grafana elsewhere, import
`grafana/dashboards/parkrr-overview.json` via **Dashboards → New → Import** and
pick your Prometheus datasource when prompted.
