# Container and pod actions

Date: 2026-10-27
Status: Approved

## Purpose

lazydev can start, stop, and restart the selected Docker container, and restart
the selected pod. Everything else stays observe-only. Phase 1 of
`2026-10-21-lazydev-design.md` left actions out of scope; this spec adds only
these three.

## Behavior

The target is the row that owns the current focus:

- Resources list, kind `containers` or `compose`: the selected container.
  Compose rows are containers.
- Resources list, kind `pods`: the selected pod.
- Detail pane, any tab except Logs: the detail selection when it is a
  container or a pod.

Services, deployments, Helm releases, images, nodes, and events ignore these
keys. An empty selection ignores them. The unfocused panel is never the target.

| Key | Container | Pod |
| --- | --- | --- |
| `s` | Docker stop | status only: pods can only be restarted with `R` |
| `S` | Docker start | same status, no API call |
| `R` | Docker restart | delete the pod |

`r` still re-probes health. There is no confirmation prompt.

These keys do nothing while the command palette or a filter is open, because
those modes take typed text. The help overlay still closes on any key. On the
Logs tab, `s` still expands stack traces, and `S` and `R` are not actions.

Docker stop and restart use the daemon's default timeout. Start uses an empty
start request. Pod delete uses the API default grace period. A pod with no
owner stays deleted; the status line still reports the delete.

## Feedback

Each key sets the status line immediately, then replaces it when that call
finishes. Another press starts another call and updates the status line to the
new intent. Whichever call finishes last writes its success or failure text,
even if it was started earlier. The Docker event watch and the Kubernetes
informers refresh the lists; the action does not edit the snapshot.

| Action | Immediate | Success | Failure |
| --- | --- | --- | --- |
| stop container `api` | `stopping api…` | `stopped api` | `stop api: <error>` |
| start container `api` | `starting api…` | `started api` | `start api: <error>` |
| restart container `api` | `restarting api…` | `restarted api` | `restart api: <error>` |
| restart pod `apps/api-1` | `restarting apps/api-1…` | `deleted apps/api-1` | `restart apps/api-1: <error>` |
| `s` or `S` on a pod | `pods can only be restarted (R)` | | |

Container text uses `Container.Name`. Pod text uses `namespace/name`.

The list footer and the non-log detail footer show `s` stop, `S` start, and
`R` restart when the target is a container, and only `R` restart when the
target is a pod. The Logs footer stays as it is. The help overlay gains an
Actions section with the same keys. The README key table matches, and the
README no longer says lazydev never changes the stack.

## Architecture

`ui.Backend` gains two methods. The UI decides that a pod cannot be started or
stopped before it calls the backend.

```go
type Action int // stop, start, restart

ContainerAction(ctx context.Context, id string, action Action) error
DeletePod(ctx context.Context, namespace, name string) error
```

`app.Backend` implements them with the Docker client already used for inspect
and the Kubernetes pod client already used for describe. The UI runs each call
as a Bubble Tea command so the event loop stays responsive. A result message
writes the success or failure status.

No new source, snapshot field, or config key.

## Error handling

API errors, including an unavailable cluster, are shown on the status line and
are not otherwise retried. A refused or missing container or pod is the same
path: the error text from the client is enough.

## Testing

UI tests drive the existing `fakeBackend` and do not need a Docker daemon or a
cluster:

- On a container row, `s`, `S`, and `R` record stop, start, and restart with
  that container's ID, and the status line shows the matching success text.
- On a pod row, `R` records a delete for that namespace and name.
- On a pod row, `s` and `S` set `pods can only be restarted (R)` and do not
  call the backend.
- On a deployment row, `s`, `S`, and `R` do not call the backend.
- With the Logs tab focused, `s` still toggles stack traces and does not call
  the backend.

`ContainerAction` and `DeletePod` stay thin wrappers over the existing clients,
same as describe, and have no separate unit test.
