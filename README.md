# browser-controller

**Kubernetes operator for the [Selenosis](https://github.com/alcounit/selenosis) platform.**
It reconciles `Browser` and `BrowserConfig` custom resources into ephemeral browser Pods —
one Pod per `Browser` — with deterministic, finalizer-backed cleanup.

[![GitHub release](https://img.shields.io/github/v/release/alcounit/browser-controller)](https://github.com/alcounit/browser-controller/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/alcounit/browser-controller.svg)](https://pkg.go.dev/github.com/alcounit/browser-controller)
[![Docker Pulls](https://img.shields.io/docker/pulls/alcounit/browser-controller.svg)](https://hub.docker.com/r/alcounit/browser-controller)
[![codecov](https://codecov.io/gh/alcounit/browser-controller/branch/main/graph/badge.svg)](https://codecov.io/gh/alcounit/browser-controller)
[![Go Report Card](https://goreportcard.com/badge/github.com/alcounit/browser-controller)](https://goreportcard.com/report/github.com/alcounit/browser-controller)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)

---

## What it does

- **Reconciles `Browser` → Pod.** Each `Browser` resource becomes **exactly one Pod** with
  the same name, built from a reusable `BrowserConfig` template.
- **Resolves configuration.** Merges `BrowserConfig` in a deterministic order
  (version → browser → template).
- **Publishes status.** Continuously updates `Browser.status` (phase, pod IP, container
  statuses, human-readable failure reason).
- **Guarantees cleanup.** A finalizer ensures the `Browser` CR and its Pod are always
  removed — on success, failure, eviction, idle, or external delete.
- **Treats pods as ephemeral.** Pods are non-restarting; failures are terminal and surfaced
  in status before deletion.

It is the only component in the stack that creates or deletes Pods.

---

## How it fits

| Component | Role |
| --- | --- |
| **[selenosis](https://github.com/alcounit/selenosis)** | Stateless Selenium / Playwright / MCP hub. |
| **[seleniferous](https://github.com/alcounit/seleniferous)** | Sidecar proxy inside each browser pod. |
| **[browser-controller](https://github.com/alcounit/browser-controller)** (this repo) | Operator that reconciles `Browser` / `BrowserConfig` CRDs into pods. Owns the CRD types. |
| **[browser-service](https://github.com/alcounit/browser-service)** | REST + SSE facade over `Browser` and `BrowserConfig` resources. |
| **[browser-ui](https://github.com/alcounit/browser-ui)** | Dashboard with live sessions + VNC. |
| **[selenosis-deploy](https://github.com/alcounit/selenosis-deploy)** | Helm chart that deploys the whole stack. **Start here.** |

---

## Quickstart

Normally deployed via the [Helm chart](https://github.com/alcounit/selenosis-deploy). To
run it standalone, apply the CRDs, RBAC, and the controller:

```bash
kubectl apply -f config/crd
kubectl apply -f config/rbac
kubectl apply -f config/controller
```

Runs in-cluster (in-cluster config) with a ServiceAccount + ClusterRole/ClusterRoleBinding
(`config/rbac`). Ready-to-use `BrowserConfig` examples live in `config/examples`.

---

## Custom resources

Two namespaced CRDs, each in its own API group:

- **`Browser`** (`brw`, group `browser.selenosis.io/v1`) — a desired browser session
  (`browserName`, `browserVersion`) plus the live Pod state (phase, IP, container
  statuses) published to `.status`.
- **`BrowserConfig`** (group `browserconfig.selenosis.io/v1`) — the browser images and pod
  templates the controller uses when creating Pods. It does **not** create Pods itself.

```yaml
apiVersion: browser.selenosis.io/v1
kind: Browser
metadata:
  name: d568aeff-a91a-449b-834b-d79bf2d6d623
  namespace: default
spec:
  browserName: chrome
  browserVersion: "120.0"
```

<details>
<summary><b>Browser CRD — full spec, status, and printer columns</b></summary>

- **Group/Version:** `browser.selenosis.io/v1` · **Kind:** `Browser` · **Scope:** Namespaced
- **Resource:** `browsers` · **Short name:** `brw` · **Categories:** `selenosis`
- **Status subresource:** enabled (`/status`)

Printer columns: `Browser` (`.spec.browserName`), `Version` (`.spec.browserVersion`),
`Phase` (`.status.phase`), `PodIP` (`.status.podIP`), `StartTime` (`.status.startTime`),
`Age`.

**Spec**
- `browserName` *(string, required, minLength=1)* — e.g. `chrome`, `firefox`.
- `browserVersion` *(string, required, minLength=1)* — e.g. `120.0`, or `latest` if supported.

**Status** (populated by the controller)
- `podIP` *(string)* — IP assigned to the pod.
- `phase` *(PodPhase)* — `Pending` / `Running` / `Succeeded` / `Failed` / `Unknown`.
- `message` *(string)* — human-readable condition description.
- `reason` *(string)* — short machine reason (e.g. `Evicted`).
- `startTime` *(Time)* — when the pod started.
- `containerStatuses` *(array)* — per-container `name`, `state`, `image`, `restartCount`, `ports`.

```bash
kubectl get brw
kubectl describe brw <name>
kubectl get brw <name> -o yaml
```

</details>

<details>
<summary><b>BrowserConfig CRD — template, browsers, command/args, merge semantics</b></summary>

`BrowserConfig` centrally manages defaults (template) and overrides them per browser name
and version. Group/Version `browserconfig.selenosis.io/v1`, Kind `BrowserConfig`,
namespaced, `/status` subresource enabled.

**`spec.template`** — base pod config applied to all browsers/versions unless overridden.
Supported fields (all optional): `labels`, `annotations`, `env`, `resources`,
`imagePullPolicy`, `volumes`, `volumeMounts`, `nodeSelector`, `affinity`, `tolerations`,
`hostAliases`, `initContainers`, `sidecars`, `privileged`, `imagePullSecrets`, `dnsConfig`,
`securityContext`, `command`, `args`, `workingDir`.

**`spec.browsers`** — required map of browser-specific, version-specific config:

```yaml
browsers:
  chrome:
    "120.0":
      image: selenium/standalone-chrome:120.0
  firefox:
    "118.0":
      image: selenium/standalone-firefox:118.0
```

**`command` / `args`** override the container `ENTRYPOINT` / `CMD` at three levels:
template (default for the main browser container), browser version (override per version),
and per sidecar/init container. `nil` inherits from the template.

```yaml
# Custom entrypoint to start a Playwright server
browsers:
  playwright-chromium:
    "1.59.1":
      image: mcr.microsoft.com/playwright:v1.59.1
      command: ["sh", "-c", "cd /opt/pw && exec ./node_modules/.bin/playwright-core run-server --port 4444 --host 0.0.0.0"]

# CLI args for an MCP image that already has an entrypoint
  playwright-mcp:
    "0.0.75":
      image: mcr.microsoft.com/playwright/mcp:v0.0.75
      args: ["--port", "8808", "--host", "0.0.0.0"]
```

**Merge semantics** — two tiers, later overrides earlier: `spec.template` (base) →
the per-version entry at `spec.browsers[name][version]`.
- `nil` fields inherit from the template.
- Maps and lists are **merged with deduplication** (override wins on key conflict), not replaced.
- Merge keys: sidecars/init containers by **name**, env vars by **name**, volumes by **name**,
  tolerations by **key**, host aliases by **IP**, image pull secrets by **name**, container
  ports by **port number**, volume mounts by **mount path**.

This keeps configuration reusable and avoids Pods rejected for duplicate entries.

**Status**: `version` (config version id), `lastUpdated` (timestamp).

</details>

---

## Reconciliation & cleanup

`BrowserConfig` is cached by the controller; reconciling a `Browser` resolves its config,
creates a Pod with the same name, tracks the Pod's lifecycle, and updates `Browser.status`.
Pods are ephemeral and non-restarting; failures are terminal.

Cleanup is enforced by a finalizer (`browserpod.selenosis.io/finalizer`) on every `Browser`
CR, so the CR **and** its Pod are always removed regardless of failure mode — and a
human-readable reason is written to `Browser.status.message` first (and surfaced as an SSE
event by browser-service) before the CR disappears.

<details>
<summary><b>Cleanup mechanism and scenario matrix</b></summary>

Two internal primitives:
- **`deletePod`** — force-deletes the Pod (`gracePeriodSeconds=0`); ignores `NotFound`.
- **`deleteBrowser`** — removes the finalizer, then `Delete`s the CR (Kubernetes completes
  deletion immediately once the finalizer is gone).

When a failure is detected, the controller handles it in a **single reconcile**:
force-delete the Pod (if any), write `status.phase=Failed` with a message, and call
`deleteBrowser`. As a safety net, a guard at the top of `Reconcile` re-runs
`deletePod` + `deleteBrowser` whenever it observes a Browser already in `Failed`, so an
interrupted cleanup is retried idempotently on the next reconcile.

| Scenario | Pod | Browser CR |
|---|---|---|
| No matching `BrowserConfig` | never created | `Failed` → **deleted** |
| Pod creation blocked by `ResourceQuota` (403) | never created | `Failed / QuotaExceeded` → **deleted** |
| `browser-pending-timeout` exceeded | never created | `Failed / PendingTimeoutExceeded` → **deleted** |
| `pod-creation-timeout` exceeded (stuck `Pending`) | force-deleted | `Failed` → **deleted** |
| Init container `Terminated` (non-zero) / non-transient `Waiting` (`ErrImagePull`, …) | force-deleted | `Failed` → **deleted** |
| Container `Terminated` / non-transient `Waiting` (`CrashLoopBackOff`, …) | force-deleted | `Failed` → **deleted** |
| Pod phase `Failed` | force-deleted | `Failed` → **deleted** |
| Critical container (`browser`/`seleniferous`) `Terminated` while `Running` | GC via OwnerReference | `deleteBrowser` → **deleted** |
| CR `DeletionTimestamp` set (`kubectl delete`) | explicit delete, waits for termination | finalizer removed after pod gone → **deleted** |
| Pod `DeletionTimestamp` set while CR alive | already terminating | `deleteBrowser` → **deleted** |
| Pod stuck `Terminating` beyond `pod-deletion-timeout` | force-deleted (best-effort) | finalizer removed → **deleted** |

Example `status.message` values: `Browser configuration not found`,
`pods "b1" is forbidden: exceeded quota: ...`, `Browser did not start within 5m0s`,
`pod container browser terminated: OOMKilled (exit code 137)`,
`Browser pod container browser failed: CrashLoopBackOff - back-off restarting failed container`.

</details>

---

## Configuration flags

| Flag | Default | Description |
|---|---|---|
| `--metrics-addr` | `:8080` | Metrics endpoint bind address. |
| `--health-probe-bind-address` | `:8081` | Health/readiness probe bind address. |
| `--enable-leader-election` | `false` | Leader election for HA deployments. |
| `--browser-pod-creation-timeout` | `5m` | Wait for a new Pod to leave `Pending` before force-delete + `Failed`. |
| `--browser-pod-deletion-timeout` | `5m` | Wait for a Pod to finish terminating before force-delete. |
| `--browser-pending-timeout` | `0` (disabled) | Cap on how long a `Browser` may stay `Pending` without a Pod (e.g. blocked by `ResourceQuota`) before `Failed / PendingTimeoutExceeded`. |
| `--max-retries` | `3` | Max retries for conflict resolution on Browser patch/status updates. |
| `--max-workers` | `4` | Max concurrent reconcile workers. |
| `--rate-limiter-base-delay` | `100ms` | Base delay for the exponential failure rate limiter. |
| `--rate-limiter-max-delay` | `30s` | Max delay for the exponential failure rate limiter. |

<details>
<summary><b><code>--browser-pending-timeout</code> in detail</b></summary>

When the cluster runs out of Pod quota (or another admission plugin blocks Pod creation),
the controller keeps retrying and the `Browser` CR stays `Pending` indefinitely. This flag
gives a deterministic deadline: if a `Browser` has lived without a Pod longer than the
configured duration, the next reconcile marks it `Failed` (reason
`PendingTimeoutExceeded`, message `Browser did not start within <timeout>`), and the
following reconcile removes it.

The timeout is measured against `Browser.metadata.creationTimestamp` (not when the
controller first saw the CR), so a controller restart does not reset the window. The
default `0` disables it and preserves retry-forever behavior.

</details>

---

## Build & generate

This project uses `make` to generate code/manifests and build the image.

```bash
make install-tools          # controller-gen, client-gen, lister-gen, informer-gen, deepcopy-gen
make generate && make manifests   # or: make all
make docker-build           # or: make deploy (build + push)
```

<details>
<summary><b>Makefile build variables</b></summary>

| Variable | Description |
| --- | --- |
| `BINARY_NAME` | Name of the produced binary (fixed: `browser-controller`). |
| `REGISTRY` | Docker registry prefix (default: `localhost:5000`). |
| `IMAGE_NAME` | Full image name, derived as `$(REGISTRY)/$(BINARY_NAME)`. |
| `VERSION` | Image version/tag (default: `develop`). |
| `EXTRA_TAGS` | Additional `-t` tags passed to `docker-push` (default: none). |
| `PLATFORM` | Target platform (default: `linux/amd64`). |
| `CONTAINER_TOOL` | Container build tool (default: `docker`). |

`REGISTRY` and `VERSION` are expected to be supplied externally so the same Makefile works
locally and in CI.

</details>

---

## Deployment

Deployed as part of the full stack via the
[selenosis-deploy](https://github.com/alcounit/selenosis-deploy) Helm chart.

---

## License

[Apache-2.0](./LICENSE)
