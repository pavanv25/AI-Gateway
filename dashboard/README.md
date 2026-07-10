# AI Gateway Dashboard

React + Recharts real-time metrics dashboard for the AI Gateway.

## Dev setup

```bash
# From the repo root — gateway must be running first
go run ./cmd/gateway

# Then, in a second terminal
cd dashboard
npm install
npm run dev        # http://localhost:5173
```

Vite proxies all `/v1/*` requests to `http://localhost:8080`, so no CORS
configuration is needed for local development.

Enter any non-empty string as the API key in the header bar (the gateway only
checks that the header is present, not that it matches a stored value).

## What's on the dashboard

| Panel | Data source |
| --- | --- |
| Stat cards (requests, errors, cache hit rate, tokens, cost) | `GET /v1/metrics` — polled every 15 s |
| Request & error rate chart (line, last 30 min) | `GET /v1/metrics/stream` — SSE, client-side bucketing by minute |
| Latency p50/p95 chart (bar) | `GET /v1/metrics` |
| Provider/model breakdown chart (horizontal bar) | `GET /v1/metrics` |
| Live event log (scrolling table, last 100 events) | `GET /v1/metrics/stream` — SSE |

The SSE connection reconnects automatically with a 2-second delay if the gateway
restarts. A 4xx response (wrong or missing API key) stops retrying and shows
"Offline" in the header.

## Build

```bash
npm run build      # output in dashboard/dist/
```
