# Scale a deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `=` set the replica count of the selected logical service's deployment, or of the selected deployment, including `0`.

**Architecture:** A footer text prompt validates a base-10 replica count. `ui.Backend.ScaleDeployment` writes the deployment scale subresource. The UI does not edit the snapshot; the deployment informer refreshes the row.

**Tech Stack:** Go, Bubble Tea text input, client-go `Deployments.GetScale` / `UpdateScale` (`autoscalingv1.Scale`).

## Global Constraints

- `=` opens the prompt for the focused logical service only when `Deployment` is set, for the focused `:deploy` row, and for the detail pane of either except the Logs tab.
- Pods, containers, compose rows, Helm releases, images, nodes, events, an empty selection, and a service with no deployment ignore `=`.
- The unfocused panel is never the target. The prompt stays closed while the palette, a filter, or help is open.
- The prompt replaces the footer, starts empty, and its placeholder is the current replica count in decimal.
- Enter trims space and accepts only digits whose value is `0` through `2147483647`. Leading zeros are allowed. A sign or decimal point is rejected.
- Empty or invalid Enter leaves the prompt open, shows `enter a whole number from 0`, and does not call the backend. Escape closes the prompt and does not change the status line.
- Status copy uses the ellipsis character `…`: `scaling <label> to N…`, `scaled <label> to N`, `scale <label>: <error>`.
- A service row or its detail pane labels with `LogicalService.Name`. A deployment row or its detail pane labels with `namespace/name`.
- The call uses a 15 second timeout. There is no confirmation. A later call's completion overwrites the status line.
- Footer hint `=` scale on a scalable row when the prompt is closed. Logs footer unchanged.
- Help and the README key table: `=` scale the selected service or deployment (`0` stops its pods).
- No new source, snapshot field, or config key. No separate unit test for the backend wrapper.

## File structure

- Modify `internal/ui/backend.go`: add `ScaleDeployment`.
- Modify `internal/ui/app_test.go`: fake method and UI tests.
- Modify `internal/app/backend.go`: `GetScale` then `UpdateScale`.
- Modify `internal/ui/app.go`: prompt, key handling, status, footer hint.
- Modify `internal/ui/help.go` and `README.md`: document `=`.

---

### Task 1: Backend contract

**Files:**
- Modify: `internal/ui/backend.go`
- Modify: `internal/ui/app_test.go` (`fakeBackend`)
- Modify: `internal/app/backend.go`

**Interfaces:**
- Produces: `Backend.ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error`

- [ ] **Step 1: Extend the interface and the fake**

Add to `Backend` in `internal/ui/backend.go`:

```go
ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error
```

In `fakeBackend`, record the call:

```go
type recordedScale struct {
	ns, name string
	replicas int32
}

// field on fakeBackend: scales []recordedScale

func (f *fakeBackend) ScaleDeployment(_ context.Context, namespace, name string, replicas int32) error {
	f.scales = append(f.scales, recordedScale{ns: namespace, name: name, replicas: replicas})
	return f.err
}
```

- [ ] **Step 2: Implement `app.Backend.ScaleDeployment`**

```go
func (b *Backend) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error {
	c := b.kube.Clients()
	if c == nil {
		return errClusterUnavailable
	}
	scale, err := c.Kube.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	scale.Spec.Replicas = replicas
	_, err = c.Kube.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	return err
}
```

`GetScale` returns `*autoscalingv1.Scale`. Do not construct a scale object by hand. Do not patch the deployment spec.

- [ ] **Step 3: Compile**

Run: `go test ./internal/ui/ ./internal/app/ -count=1 -run TestDoesNotExist`

Expected: packages compile. The named test is absent, so the command exits non-zero with `no tests to run` only if compilation succeeded. A compile error names the missing method.

### Task 2: Prompt and scale calls

**Files:**
- Modify: `internal/ui/app.go`
- Test: `internal/ui/app_test.go`

**Interfaces:**
- Consumes: `ScaleDeployment`
- Produces: footer prompt, `actionResultMsg` texts `scaling` / `scaled` / `scale`

Reuse `actionResultMsg` for the async result. Do not add a second message type.

- [ ] **Step 1: Write the failing tests**

`newTestModel` auto-discovers service `alpha` from deployment `apps/alpha` with 1 replica. Use that for the service test. `openResource(m, "deploy")` selects that same deployment.

```go
func TestScaleServiceToZero(t *testing.T) {
	m, backend := newTestModel(t)
	m.Update(runes("="))
	if !m.scale.active || m.scale.input.Placeholder != "1" {
		t.Fatalf("prompt active %v placeholder %q", m.scale.active, m.scale.input.Placeholder)
	}
	m.scale.input.SetValue("00")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.scale.active || !strings.Contains(m.View(), "scaling alpha to 0") {
		t.Fatalf("active %v status %q", m.scale.active, m.status)
	}
	m.Update(findMsg[actionResultMsg](t, cmd))
	if !strings.Contains(m.View(), "scaled alpha to 0") {
		t.Fatalf("status %q", m.status)
	}
	if len(backend.scales) != 1 || backend.scales[0] != (recordedScale{ns: "apps", name: "alpha", replicas: 0}) {
		t.Fatalf("scales %+v", backend.scales)
	}
}

func TestScaleDeploymentRow(t *testing.T) {
	m, backend := newTestModel(t)
	openResource(m, "deploy")
	m.Update(runes("="))
	m.scale.input.SetValue("3")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(findMsg[actionResultMsg](t, cmd))
	if len(backend.scales) != 1 || backend.scales[0].replicas != 3 || backend.scales[0].ns != "apps" || backend.scales[0].name != "alpha" {
		t.Fatalf("scales %+v", backend.scales)
	}
	if !strings.Contains(m.View(), "scaled apps/alpha to 3") {
		t.Fatalf("status %q", m.status)
	}
}

func TestServiceWithoutDeploymentIgnoresScale(t *testing.T) {
	backend := &fakeBackend{}
	cfg := &config.Config{Services: []config.Service{{Name: "redis", Compose: "redis"}}}
	snap := testSnapshot()
	m := New(cfg, "test", fakeStore{snap}, backend)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.Update(snapshotMsg{snap: snap})
	m.Update(runes("="))
	if m.scale.active || len(backend.scales) != 0 {
		t.Fatalf("active %v scales %+v", m.scale.active, backend.scales)
	}
}

func TestScalePromptRejectsInvalid(t *testing.T) {
	m, backend := newTestModel(t)
	m.Update(runes("="))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.scale.active || !strings.Contains(m.View(), "enter a whole number from 0") {
		t.Fatalf("empty enter active %v err %q", m.scale.active, m.scale.err)
	}
	for _, bad := range []string{"x", "-1", "1.5", "2147483648"} {
		m.scale.input.SetValue(bad)
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if !m.scale.active || len(backend.scales) != 0 || !strings.Contains(m.View(), "enter a whole number from 0") {
			t.Fatalf("%q active %v scales %+v err %q", bad, m.scale.active, backend.scales, m.scale.err)
		}
	}
}

func TestDetailTabScalesService(t *testing.T) {
	m, backend := newTestModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(runes("]"))
	if m.focus != focusDetail || m.detail.tab == tabLogs {
		t.Fatalf("focus %v tab %v", m.focus, m.detail.tab)
	}
	m.Update(runes("="))
	m.scale.input.SetValue("2")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(findMsg[actionResultMsg](t, cmd))
	if len(backend.scales) != 1 || backend.scales[0].replicas != 2 {
		t.Fatalf("scales %+v", backend.scales)
	}
	if !strings.Contains(m.View(), "scaled alpha to 2") {
		t.Fatalf("status %q", m.status)
	}
}

func TestScalePromptEscape(t *testing.T) {
	m, backend := newTestModel(t)
	m.Update(runes("="))
	m.scale.input.SetValue("0")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.scale.active || m.status != "" || len(backend.scales) != 0 {
		t.Fatalf("active %v status %q scales %+v", m.scale.active, m.status, backend.scales)
	}
}

func TestLogsTabIgnoresScale(t *testing.T) {
	m, backend := newTestModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != focusDetail || m.detail.tab != tabLogs {
		t.Fatalf("focus %v tab %v", m.focus, m.detail.tab)
	}
	m.Update(runes("="))
	if m.scale.active || len(backend.scales) != 0 {
		t.Fatalf("active %v scales %+v", m.scale.active, backend.scales)
	}
}

func TestPodRowIgnoresScale(t *testing.T) {
	m, backend := newTestModel(t)
	openResource(m, "pods")
	m.Update(runes("="))
	if m.scale.active || len(backend.scales) != 0 {
		t.Fatalf("active %v scales %+v", m.scale.active, backend.scales)
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/ui/ -count=1 -run 'TestScale|TestServiceWithout|TestLogsTabIgnores|TestPodRowIgnores'`

Expected: FAIL to compile because `m.scale` does not exist.

- [ ] **Step 3: Implement the prompt**

Add a `scalePrompt` on `Model` with a `textinput.Model`, `active`, `err`, and the captured `namespace`, `name`, and `label`. Prompt character `=`, placeholder set when opening, value cleared, focused.

`textInputActive` returns true when the prompt is active.

In `handleKey`, before the global key switch and after the palette check, if the prompt is active call `handleScaleKey` and return. `handleScaleKey`: Escape closes and returns nil. Enter parses with `parseReplicas`; on failure set `err` to `enter a whole number from 0` and return nil. On success close the prompt, set `m.status` to `scaling <label> to <n>…`, and return a command that calls `ScaleDeployment` with `context.WithTimeout` of 15 seconds. Success message `scaled <label> to <n>`. Failure message `scale <label>: <err>`. Send `actionResultMsg`.

`parseReplicas` trims space, rejects an empty string or any non-digit, and uses `strconv.ParseInt(s, 10, 32)`.

When the prompt is active, `footer` returns the input view plus `scale.err`, using the same pattern as the palette, and does not show the hint line.

Open the prompt on `=` only when `textInputActive` is false, the Logs tab is not focused, and `scaleTarget` returns a deployment. Set the placeholder to the current replica count in decimal. Capture namespace, name, and label at open time so a later snapshot cannot retarget the call.

`scaleTarget` reads the focused services row, the focused deploy row, or the detail selection. It returns false for every other focus and kind.

Do not edit `m.snap` after a successful scale.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/ui/ -count=1 -run 'TestScale|TestServiceWithout|TestLogsTabIgnores|TestPodRowIgnores'`

Expected: PASS

### Task 3: Footer, help, README

**Files:**
- Modify: `internal/ui/app.go` (`footer`)
- Modify: `internal/ui/help.go`
- Modify: `README.md`
- Test: `internal/ui/app_test.go`

- [ ] **Step 1: Write the failing footer test**

```go
func TestScaleFooterHint(t *testing.T) {
	m, _ := newTestModel(t)
	footer := plainFooter(m.View())
	if !strings.Contains(footer, "scale") {
		t.Fatalf("service footer %q", footer)
	}
	openResource(m, "deploy")
	if footer = plainFooter(m.View()); !strings.Contains(footer, "scale") {
		t.Fatalf("deploy footer %q", footer)
	}
	openResource(m, "pods")
	footer = plainFooter(m.View())
	if strings.Contains(footer, "scale") {
		t.Fatalf("pod footer %q", footer)
	}
	m.Update(runes("?"))
	if !strings.Contains(m.View(), "0 stops its pods") {
		t.Fatal("help missing scale key")
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/ui/ -count=1 -run TestScaleFooterHint`

Expected: FAIL because the footer has no `scale` hint.

- [ ] **Step 3: Show the hint and document the key**

Prepend `{"=", "scale"}` to the list footer and the non-log detail footer when `scaleTarget` succeeds. Leave the Logs footer unchanged.

Add to the Actions help section: `=` / `scale the selected service or deployment (0 stops its pods)`.

Add the same row to the README key table, after `R`.

- [ ] **Step 4: Run the footer test**

Run: `go test ./internal/ui/ -count=1 -run TestScaleFooterHint`

Expected: PASS

### Task 4: Verify

- [ ] **Step 1: Run the UI package and the full suite**

Run: `go test -race ./internal/ui/ ./internal/app/ -count=1`

Expected: PASS

Run: `make test`

Expected: PASS
