# Container and pod actions implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Start, stop, and restart the selected Docker container, and restart the selected pod, from the resource list and from non-log detail tabs.

**Architecture:** `ui.Backend` gains `ContainerAction` and `DeletePod`. The UI chooses the focused container or pod, refuses pod start/stop locally, and runs the backend call in a Bubble Tea command. Docker events and Kubernetes informers refresh the lists. No snapshot writes.

**Tech Stack:** Go, Bubble Tea, Docker Engine API v28 (`container.StopOptions` / `container.StartOptions`), client-go pod delete.

## Global Constraints

- Targets: selected container on `:containers` or `:compose`, selected pod on `:pods`, or the detail selection when it is a container or pod and the tab is not Logs.
- Services, deployments, Helm releases, images, nodes, events, and an empty selection ignore `s`, `S`, and `R`.
- The unfocused panel is never the target.
- `s` stop, `S` start, `R` restart. `r` still re-probes health.
- Pod `s` and `S` set status `pods can only be restarted (R)` and do not call the backend.
- Pod `R` deletes the pod with `metav1.DeleteOptions{}` (API default grace).
- Docker stop and restart use `container.StopOptions{}` (nil timeout, daemon default). Start uses `container.StartOptions{}`.
- No confirmation prompt.
- Palette, filter, and the help overlay do not run these actions. On the Logs tab, `s` still expands stack traces, and `S` and `R` are not actions.
- Status copy, including the ellipsis character `…`:
  - `stopping <name>…` / `stopped <name>` / `stop <name>: <error>`
  - `starting <name>…` / `started <name>` / `start <name>: <error>`
  - `restarting <name>…` / `restarted <name>` / `restart <name>: <error>`
  - pod restart success text is `deleted <namespace>/<name>`
- Container text uses `Container.Name`. Pod text uses `namespace/name`.
- A later key updates the status line immediately. Whichever call finishes last overwrites it.
- Footer: container shows `s` stop, `S` start, `R` restart. Pod shows only `R` restart. Logs footer is unchanged.
- Help gains an Actions section. README key table matches, and the README no longer says lazydev never changes the stack.
- No new source, snapshot field, or config key. No separate unit test for the backend wrappers.

---

### Task 1: Backend contract

**Files:**
- Modify: `internal/ui/backend.go`
- Modify: `internal/ui/app_test.go` (`fakeBackend`)
- Modify: `internal/app/backend.go`

**Interfaces:**
- Produces: `ui.Action` (`ActionStop`, `ActionStart`, `ActionRestart`), `Backend.ContainerAction(ctx, id, action) error`, `Backend.DeletePod(ctx, namespace, name) error`

- [x] Add `Action` and the two `Backend` methods.
- [x] Record calls on `fakeBackend`.
- [x] Implement `app.Backend` with the Docker client and `Pods().Delete`.

### Task 2: Key handling and status

**Files:**
- Modify: `internal/ui/app.go`
- Test: `internal/ui/app_test.go`

**Interfaces:**
- Consumes: `ContainerAction`, `DeletePod`, `ActionStop`, `ActionStart`, `ActionRestart`
- Produces: `actionResultMsg`, status line updates from `s` / `S` / `R`

- [x] Write failing UI tests for container stop/start/restart, compose stop, pod restart, pod start/stop refusal, deployment ignore, logs `s` and `R`, filter `s`, detail-tab start, and a Docker error.
- [x] Run `go test ./internal/ui/ -run 'TestContainer|TestPod|TestDeployment|TestLogsTab|TestFilter' -count=1` and confirm failure.
- [x] Handle the keys after the focused view, skip the Logs tab, run the call with a 45s timeout, and apply `actionResultMsg`.

### Task 3: Footer, help, README

**Files:**
- Modify: `internal/ui/app.go` (`footer`)
- Modify: `internal/ui/help.go`
- Modify: `README.md`
- Test: `internal/ui/app_test.go`

- [x] Show action hints on the list footer and the non-log detail footer.
- [x] Add the Actions help section.
- [x] Update the README purpose line and key table.
- [x] Assert the container footer contains `s` stop / `S` start / `R` restart and the pod footer contains `R` restart without `start`.

### Task 4: Verify

- [x] Run `go test -race ./internal/ui/ ./internal/app/ -count=1`
- [x] Run `make test`
