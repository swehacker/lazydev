# Scale a deployment

Date: 2026-10-27
Status: Approved

## Purpose

lazydev can set the replica count of a deployment, including `0`, which means
no pods are desired. The target is the deployment behind the selected logical
service, or the selected deployment row. Both change the same object.

## Where it works

`=` opens the prompt for the row that owns the current focus:

- Services list: the selected logical service, only when `Deployment` is set.
  A service with no deployment ignores `=`.
- Resources list, kind `deploy`: the selected deployment.
- Detail pane, any tab except Logs: the detail selection when it is a logical
  service with a deployment, or a deployment.

Pods, containers, compose rows, Helm releases, images, nodes, and events ignore
`=`. An empty selection ignores it. The unfocused panel is never the target.
The prompt does not open while the command palette, a filter, or the help
overlay is open. On the Logs tab, `=` is not a scale action.

## Prompt

The prompt replaces the footer, in the same way `:` and `/` do. It starts
empty. The placeholder is the current replica count in decimal, from
`Deployment.Replicas` on a deployment row and from the service's deployment
replica count on a service row.

Enter trims surrounding space and accepts the text only when what remains is
digits and its value is from `0` through `2147483647`. Leading zeros are
allowed, so `0` and `00` both mean zero replicas. A sign, a decimal point, or
any other character is rejected.

An empty or invalid entry leaves the prompt open and shows
`enter a whole number from 0`. It does not call the API. Escape closes the
prompt and does not change the deployment or the status line. Keys typed while
the prompt is open go to the input, so a second `=` does not open another
prompt.

## Scale call

Enter closes the prompt and sets the deployment's scale subresource, the same
API as `kubectl scale`. `app.Backend` loads the scale with `GetScale`, sets
`spec.replicas`, and writes it with `UpdateScale`. A missing cluster returns
the existing unavailable-cluster error.

The label is the logical service name when the service row or its detail pane
started the prompt. It is `namespace/name` when a deployment row or its detail
pane started the prompt.

| Result | Status line |
| --- | --- |
| call started for service `api` at `0` | `scaling api to 0…` |
| call accepted | `scaled api to 0` |
| call failed | `scale api: <error>` |

A deployment row uses `namespace/name` in those three texts. The call runs in a
Bubble Tea command with a 15 second timeout. The client error, including a
timeout or an unavailable cluster, is the `<error>` text. There is no
confirmation step.

After the prompt closes, `=` can start another scale. Whichever call finishes
last writes its success or failure text, even if it was started earlier. The
deployment informer refreshes the lists. The action does not edit the snapshot.

While a scalable row is focused and the prompt is closed, the list footer and
the non-log detail footer show `=` scale. The Logs footer stays as it is. The
help overlay and the README key table gain `=` scale the selected service or
deployment (`0` stops its pods).

## Architecture

`ui.Backend` gains one method. The UI validates the count before calling it.

```go
ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error
```

No new source, snapshot field, or config key.

## Error handling

Invalid input is local to the prompt. API errors are shown on the status line
and are not retried. A missing deployment or a refused scale uses the client
error text.

## Testing

UI tests drive the fake backend and do not need a cluster:

- On a service with a deployment, `=`, `0`, and Enter record a scale for that
  deployment's namespace and name at `0`, and the status line shows
  `scaled <service> to 0`.
- On a deployment row, `=`, `3`, and Enter record replicas `3` and the status
  line shows `scaled <namespace>/<name> to 3`.
- On a service with no deployment, `=` does not open a prompt and does not
  call the backend.
- Enter on an empty prompt and Enter on `x` leave the prompt open, show
  `enter a whole number from 0`, and do not call the backend.
- Escape closes the prompt and does not call the backend.
- With the Logs tab focused on a scalable selection, `=` does not open a prompt
  and does not call the backend.
- On a pod row, `=` does not call the backend.

`ScaleDeployment` stays a thin wrapper over `GetScale` and `UpdateScale`, same
as the other backend actions, and has no separate unit test.
