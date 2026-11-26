# thrum-collector

Watches a Kubernetes cluster with **read-only** access and publishes a normalized health state for the Thrum sound engine, or for anything else that wants it.

## Run it

Requires Go 1.24 or newer.

```sh
cd collector
go run ./cmd/thrum-collector            # uses your current kubeconfig context
```

Then start the app (`npm run dev` in the repository root) and press **Connect to cluster**.

| Flag | Meaning |
|---|---|
| `--kubeconfig` | Path to a kubeconfig. Defaults to `$KUBECONFIG`, then `~/.kube/config`. |
| `--context` | Kubeconfig context. Defaults to the current one. |
| `--namespace` | Watch pods and deployments in one namespace only. |
| `--exclude-namespaces` | Comma-separated namespaces to ignore. |
| `--listen` | Address to serve on. Default `127.0.0.1:7878`. |
| `--interval` | How often to publish. Default `2s`. |
| `--config` | YAML file, see [`thrum.example.yaml`](thrum.example.yaml). |
| `--once` | Print the current state as JSON and exit. |

### No cluster handy?

A stand-in API server ships with the repository:

```sh
go run ./hack/fakeapi --scenario failing &     # healthy, busy or failing
go run ./cmd/thrum-collector --kubeconfig /tmp/thrum-fake.kubeconfig
```

## What it reads

Nodes, pods and deployments (list and watch), node usage from the Metrics API if metrics-server is installed, and one timed request to `/readyz`. The full permission set is in [`deploy/rbac.yaml`](../deploy/rbac.yaml). It contains no verbs that change anything.

Clusters without metrics-server work. CPU and memory are then reported as unavailable, and health is unaffected.

Network and disk activity need Prometheus, which is planned for a later phase. Until then they are reported as 0.

## The health model

Busy is not unhealthy. CPU and memory are reported as **load** and never lower the health score. Only **conditions** do:

| Condition | Counts |
|---|---|
| `pendingPods` | Pods Pending for longer than the grace period |
| `crashLoop` | Pods with a container in `CrashLoopBackOff` |
| `failedWorkloads` | Deployments below minimum availability or with a stalled rollout, pods that cannot pull or create their containers, recently failed pods |
| `nodeNotReady` | Nodes that are NotReady. Nodes under memory, disk or PID pressure count half |
| `apiLatency` | API server round-trip time. An unreachable API server caps health at 25 |

Each condition is a 0 to 1 severity that works at any cluster size: one bad node out of three is serious by proportion, and one out of five hundred still registers by count. Health is 0 to 100. Each condition removes its weight's share of what remains, so several symptoms of one outage do not immediately hit zero. Weights and thresholds are configurable.

## API

| Endpoint | Returns |
|---|---|
| `GET /healthz` | `ok` when the collector is running |
| `GET /v1/state` | The latest state as JSON |
| `GET /v1/stream` | Server-sent events: `state` on every update, `event` for restarts, deployments, scaling and failures |

```json
{
  "cluster": "homelab",
  "time": "2026-01-01T12:00:00Z",
  "state": {
    "health": 86,
    "load": { "cpu": 0.42, "memory": 0.61, "network": 0, "disk": 0 },
    "conditions": { "pendingPods": 0, "crashLoop": 0.25, "nodeNotReady": 0, "apiLatency": 0, "failedWorkloads": 0 }
  },
  "detail": { "nodesTotal": 3, "nodesNotReady": 0, "podsTotal": 41, "podsCrashLooping": 1 }
}
```

## Security

- The API is read-only and serves on loopback by default.
- Browsers may only read it from loopback origins, the desktop app, or origins listed in `allowOrigins`. This stops an arbitrary website from reading your cluster's health through your browser.
- To serve on another address, set `THRUM_TOKEN`. The collector refuses to start on a non-loopback address without it. Clients send it as `Authorization: Bearer <token>` or `?token=<token>`. Use TLS in front of it if the network is not trusted.

## Test

```sh
go vet ./...
go test -race ./...
```
