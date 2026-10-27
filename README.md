# lazydev

A terminal UI for the local development stack: one screen for the k3d cluster, Docker Compose
infrastructure, local images and the Docker engine (Colima). Think lazydocker + k9s, organised
around *logical services* instead of raw resources.

lazydev observes the stack. It can start, stop, and restart the selected container, and restart
the selected pod.

## What it shows

- **Services** — each configured service rolls up its pods or compose containers into one status
  (Healthy / Degraded / Down / Completed) with ready count, restarts, CPU/memory, HTTP/TCP health
  and image drift.
- **Image drift** — whether the local `:local` image is missing, not imported into k3d, imported
  but stale (rebuilt since the last import), not rolled out (pods still run an older import) or in sync.
- **Engine** — Colima CPU, memory and disk usage of the VM, plus k3d node and compose counts.
- **Resources** (`:` palette) — pods, deployments, Helm releases, compose services, containers,
  images, nodes and Kubernetes events.
- **Logs** — follow, search, level filter, multi-pod merge, JSON pretty-printing, grouped stack
  traces, previous-container logs and interleaved Kubernetes events.

## Install

```sh
brew tap swehacker/tap
brew install --cask swehacker/tap/lazydev
```

From source (Go 1.27+):

```sh
go install github.com/swehacker/lazydev/cmd/lazydev@latest
```

## Usage

```sh
lazydev                          # finds ./lazydev.yaml, the git root's lazydev.yaml, or ~/.config/lazydev/config.yaml
lazydev --config path/to/lazydev.yaml
lazydev --context k3d-other      # override the kube context
```

Without a config file lazydev runs in auto mode: one service per deployment in the current kube
context and one per compose service.

The Docker host is resolved like the docker CLI (`DOCKER_HOST`, `DOCKER_CONTEXT`, then the current
context in `~/.docker/config.json`), so Colima works without extra setup.

## Configuration

Commit a `lazydev.yaml` next to the stack it describes.

```yaml
cluster:
  k3dName: dev                     # kube context defaults to k3d-<k3dName>
compose:
  project: dev-infra
namespaces: [apps]                 # informers are limited to these namespaces
health:
  interval: 10s
  timeout: 3s
  insecureSkipVerify: true         # default; local certificates are self-signed

services:
  - name: api
    k8s: { namespace: apps, selector: app=api }
    image: api:local               # enables drift detection
    health: https://api.example.local/actuator/health
  - name: postgres
    compose: postgres
    health: tcp://localhost:5432
```

A service needs `k8s` (namespace + label selector, optionally `deployment`) and/or `compose`
(the compose service name). `health` accepts `http://`, `https://` (2xx/3xx is healthy, redirects
are not followed) and `tcp://host:port`.

## Keys

| Key | Action |
| --- | --- |
| `j` `k` / `g` `G` | move / top, bottom |
| `enter`, `esc` | open detail / back |
| `1` `2` `tab` | focus Services / Resources / cycle |
| `[` `]` | previous / next detail tab (Logs, Events, Describe, Env, Stats) |
| `z` | zoom the detail pane |
| `/` | filter lists, search logs (`n` / `N` jump, `F` only matches) |
| `:` | command palette: `pods`, `deploy`, `helm`, `compose`, `containers`, `images`, `nodes`, `events`, `ns <name\|all>`, `services` |
| `r` | re-run health checks |
| `s` | stop the selected container |
| `S` | start the selected container |
| `R` | restart the selected container or pod |
| `=` | scale the selected service or deployment (0 stops its pods) |
| `?` | help |

In logs: `space` follow/pause, `l` minimum level, `s` expand stacks, `p` previous container,
`e` events, `t` timestamps, `w` wrap, `c` clear.

## Troubleshooting

- **Log following stops with "too many open files"** — the kubelet in the Colima VM ran out of
  inotify instances. Raise the limit: `colima ssh -- sudo sysctl -w fs.inotify.max_user_instances=1024`.
- **Kube context not found** — lazydev shows the error in the header and keeps retrying; check
  `kubectl config get-contexts` or pass `--context`.

## Development

```sh
make build     # bin/lazydev
make test      # go test -race ./...
make lint      # golangci-lint
make snapshot  # local GoReleaser build into dist/
```

Releases: tag `vX.Y.Z`, export `GITHUB_TOKEN`, and run `goreleaser release --clean`.
This publishes archives to the GitHub release and updates the cask in `swehacker/homebrew-tap`.
