[![GitHub release](https://img.shields.io/github/v/release/alcounit/browser-controller)](https://github.com/alcounit/browser-controller/releases) [![Go Reference](https://pkg.go.dev/badge/github.com/alcounit/browser-controller.svg)](https://pkg.go.dev/github.com/alcounit/browser-controller) [![Docker Pulls](https://img.shields.io/docker/pulls/alcounit/browser-controller.svg)](https://hub.docker.com/r/alcounit/browser-controller)

# Browser Controller

Browser Controller is a Kubernetes controller for the **Selenosis** ecosystem.  
It manages `Browser` and `BrowserConfig` custom resources and is responsible for creating, monitoring, and cleaning up ephemeral browser Pods.

The controller is designed for deterministic browser provisioning with strict lifecycle management.

---

## Overview

- **Browser** — runtime resource representing a single browser instance.
- **BrowserConfig** — configuration resource defining browser images and pod templates.
- **Controller** — resolves configuration, creates Pods, tracks their lifecycle, and updates status.

Each `Browser` resource results in **exactly one Pod** with the same name.

---

## What the Controller Does

- Registers `Browser` and `BrowserConfig` CRDs
- Reconciles `Browser` resources into Pods
- Resolves configuration using `BrowserConfig` (template + overrides)
- Updates `Browser.status` with runtime information
- Ensures proper cleanup via finalizers
- Exposes health, readiness, and metrics endpoints

---

## Requirements & RBAC

- Runs inside a Kubernetes cluster (in-cluster config)
- Uses a ServiceAccount with ClusterRole / ClusterRoleBinding
- RBAC manifests are located in `config/rbac`
- Examples assume the `default` namespace

---

## Quickstart

Apply CRDs, RBAC, and deploy the controller:

```bash
kubectl apply -f config/crd
kubectl apply -f config/rbac
kubectl apply -f config/controller
```

BrowserConfig examples `config/examples`

---

## Browser CRD

`Browser` is a namespaced CustomResource that defines a desired browser session (browser type and version) and exposes the actual runtime state of the underlying Kubernetes Pod (phase, IP, container details). It is used by the browser-controller to manage the full lifecycle of browser pods.

### API Overview

- **Group/Version:** `selenosis.io/v1` (adjust if your API group differs)
- **Kind:** `Browser`
- **Scope:** Namespaced
- **Resource:** `browsers`
- **Short name:** `brw`
- **Categories:** `selenosis`
- **Status subresource:** enabled (`/status`)

The CRD defines additional printer columns for quick inspection:

- **Browser**: `.spec.browserName`
- **Version**: `.spec.browserVersion`
- **Phase**: `.status.phase`
- **PodIP**: `.status.podIP`
- **StartTime**: `.status.startTime`
- **Age**: `.metadata.creationTimestamp`

Example:

```bash
kubectl get browsers
kubectl get brw
```

### Spec

`spec` describes the desired browser configuration:

- **browserName** *(string, required, minLength=1)*  
  Name of the browser to run (for example: `chrome`, `firefox`).

- **browserVersion** *(string, required, minLength=1)*  
  Browser version to use (for example: `91.0`, `120.0`, or `latest` if supported by the controller).

### Status

`status` is populated by the controller and reflects the observed state of the browser pod:

- **podIP** *(string, optional)*  
  IP address assigned to the pod.

- **phase** *(PodPhase, optional)*  
  Current lifecycle phase of the pod (`Pending`, `Running`, `Succeeded`, `Failed`, `Unknown`).

- **message** *(string, optional)*  
  Human-readable description of the current condition.

- **reason** *(string, optional)*  
  Short, machine-friendly reason (for example: `Evicted`).

- **startTime** *(Time, optional)*  
  Timestamp when the pod was started.

- **containerStatuses** *(array, optional)*  
  Detailed status for each container:
  - **name** — container name
  - **state** — current container state (`Pending`, `Running`, `Failed`)
  - **image** — container image
  - **restartCount** — number of restarts
  - **ports** — exposed ports (container/host, protocol, name)

### Minimal Manifest Example

```yaml
apiVersion: selenosis.io/v1 
kind: Browser
metadata:
  name: d568aeff-a91a-449b-834b-d79bf2d6d623
  namespace: default
spec:
  browserName: chrome
  browserVersion: "120.0"
```

Apply and inspect:

```bash
kubectl apply -f browser.yaml
kubectl get brw 
kubectl describe brw d568aeff-a91a-449b-834b-d79bf2d6d623
kubectl get brw d568aeff-a91a-449b-834b-d79bf2d6d623 -o yaml
```

### Expected Controller Behavior

- Based on `spec.browserName` and `spec.browserVersion`, the controller creates and manages a dedicated browser pod.
- Runtime details (IP, phase, start time, container statuses) are continuously published to `.status`, allowing UIs and clients to quickly determine browser availability and health.
---
## BrowserConfig CRD

`BrowserConfig` is a namespaced CustomResource that defines **browser images and pod-level configuration templates** used by the browser-controller when creating browser pods.  
It allows you to centrally manage defaults (template) and override them per **browser name** and **browser version**.

This CRD does **not** create pods by itself. Instead, it acts as a configuration source consumed by the browser-controller.

---

### API Overview

- **Group/Version:** `selenosis.io/v1` (adjust if your API group differs)
- **Kind:** `BrowserConfig`
- **Scope:** Namespaced
- **Status subresource:** enabled (`/status`)

---

### Purpose

`BrowserConfig` provides:

- A **global pod template** applied to all browsers and versions
- Per-browser and per-version **override capabilities**
- A deterministic **merge strategy** (version → browser → template)
- Centralized control over:
  - Browser images
  - Resources
  - Environment variables
  - Volumes and mounts
  - Sidecars and init containers
  - Scheduling and security settings

---

### Spec

#### Template

`spec.template` defines a **base pod configuration** applied to all browsers and versions unless explicitly overridden.

Supported fields include:

- `labels`, `annotations`
- `env`
- `resources`
- `imagePullPolicy`
- `volumes`, `volumeMounts`
- `nodeSelector`, `affinity`, `tolerations`
- `hostAliases`
- `initContainers`
- `sidecars`
- `privileged`
- `imagePullSecrets`
- `dnsConfig`
- `securityContext`
- `workingDir`

All fields are optional.

---

#### Browsers

`spec.browsers` is a required map that defines browser-specific and version-specific configuration.

Structure:

```yaml
browsers:
  <browserName>:
    <browserVersion>:
      image: <container-image>
      ...
```

Example:

```yaml
browsers:
  chrome:
    "120.0":
      image: selenium/standalone-chrome:120.0
    "121.0":
      image: selenium/standalone-chrome:121.0
  firefox:
    "118.0":
      image: selenium/standalone-firefox:118.0
```

Each browser version supports the same override fields as the template.

---

### Merge Semantics

Configuration is merged in the following order (later overrides earlier):

1. **Template**
2. **Browser-level version config**
3. **Explicit version overrides**

Rules:

- `nil` fields inherit values from the template
- Maps and lists are **merged with deduplication**, not replaced — override wins on key conflict
- Sidecars and init containers are merged by **name**
- Environment variables are merged by **variable name**
- Volumes are merged by **name**
- Tolerations are merged by **key**
- Host aliases are merged by **IP**
- Image pull secrets are merged by **name**
- Container ports are merged by **container port number**
- Volume mounts are merged by **mount path**

This ensures predictable and reusable configuration without duplication or rejected Pods due to duplicate entries.

---

### Status

`status` reflects metadata about the effective configuration:

- **version** *(string)* — current configuration version identifier
- **lastUpdated** *(timestamp)* — last update time

---

### Minimal Example

```yaml
apiVersion: selenosis.io/v1
kind: BrowserConfig
metadata:
  name: default-browser-config
  namespace: default
spec:
  template:
    resources:
      requests:
        cpu: "500m"
        memory: "1Gi"
    env:
      - name: TZ
        value: UTC

  browsers:
    chrome:
      "120.0":
        image: selenium/standalone-chrome:120.0
```

Apply and inspect:

```bash
kubectl apply -f browserconfig.yaml
kubectl get browserconfig -n
kubectl describe browserconfig default-browser-config 
kubectl get browserconfig default-browser-config -o yaml
```

---

## Reconciliation Model (Summary)

- `BrowserConfig` is loaded and cached by the controller
- `Browser` reconciliation:
  - resolves configuration
  - creates a Pod with the same name
  - tracks Pod lifecycle
  - updates `Browser.status`
- Pods are **non-restarting** and treated as ephemeral
- Failures are terminal and reflected in `Browser.status`

---

## Resource Cleanup

The controller is responsible for ensuring that both the `Browser` CR and its associated Pod are always cleaned up, regardless of the failure mode. Cleanup is enforced through a **finalizer** (`browserpod.selenosis.io/finalizer`) placed on every `Browser` CR.

### Mechanism

Every `Browser` CR receives the finalizer on creation. The controller uses two internal primitives:

- **`deletePod`** — force-deletes the Pod with `gracePeriodSeconds=0`. Ignores `NotFound` (safe to call even if Pod is already gone).
- **`deleteBrowser`** — removes the finalizer from the `Browser` CR, then calls `Delete` on the CR. After the finalizer is removed Kubernetes completes the deletion immediately.

Most failure paths follow a **two-step process** across two reconcile cycles:

1. **First reconcile** — detects the failure, force-deletes the Pod, sets `Browser.status.phase=Failed` with a descriptive message.
2. **Second reconcile** — sees `status.phase=Failed`, calls `deletePod` (no-op if already gone) and `deleteBrowser`, which removes the CR.

### Cleanup Scenarios

| Scenario | Pod | Browser CR |
|---|---|---|
| No matching `BrowserConfig` | never created | set to `Failed` → next reconcile: **deleted** |
| Pod creation blocked by `ResourceQuota` (403 Forbidden) | never created | set to `Failed / QuotaExceeded` immediately → next reconcile: **deleted** |
| `browserPendingTimeout` exceeded (Browser stayed `Pending` without a Pod longer than the timeout) | never created | set to `Failed / PendingTimeoutExceeded` → next reconcile: **deleted** |
| `podCreationTimeout` exceeded (pod stuck `Pending` > 5 min) — applies to both init containers and regular containers | force-deleted (grace=0) | set to `Failed` → next reconcile: **deleted** |
| `PodPending` + init container `Terminated` with non-zero exit code | force-deleted (grace=0) | set to `Failed` → next reconcile: **deleted** |
| `PodPending` + init container `Waiting` with non-transient reason (`ErrImagePull`, `ImagePullBackOff`, etc.) | force-deleted (grace=0) | set to `Failed` → next reconcile: **deleted** |
| `PodPending` + container `Terminated` | force-deleted (grace=0) | set to `Failed` → next reconcile: **deleted** |
| `PodPending` + container `Waiting` with non-transient reason (`CrashLoopBackOff`, `ErrImagePull`, `ImagePullBackOff`, etc.) | force-deleted (grace=0) | set to `Failed` → next reconcile: **deleted** |
| Pod phase `Failed` | force-deleted (grace=0) | set to `Failed` → next reconcile: **deleted** |
| `Browser.status.phase=Failed` (failed early exit — any of the above on the next reconcile) | force-deleted (grace=0) | finalizer removed → `Delete` → **deleted** |
| Critical container (`browser` or `seleniferous`) `Terminated` while pod is `Running` | **deleted** via OwnerReference GC after CR deletion | `deleteBrowser` → finalizer removed → **deleted** |
| `Browser` CR `DeletionTimestamp` set (external `kubectl delete`) | explicit `Delete` in `handleDeletion`, waits for pod termination | finalizer removed after pod is gone → **deleted** |
| Pod `DeletionTimestamp` set while CR is alive | already terminating | `deleteBrowser` triggered → **deleted** |
| Pod stuck `Terminating` beyond `podDeletionTimeout` (5 min) | force-deleted (grace=0, best-effort) | finalizer removed regardless → **deleted** |

### `Browser.status.message` on Failure

Every failure path writes a human-readable message to `Browser.status.message` before deletion:

| Cause | Example message |
|---|---|
| No `BrowserConfig` | `Browser configuration not found` |
| Quota exceeded | `pods "b1" is forbidden: exceeded quota: ...` |
| Pending timeout exceeded | `Browser did not start within 5m0s` |
| Creation timeout | `pod creation timeout exceeded after 5m0s, container browser: ContainerCreating` |
| Init container creation timeout | `pod creation timeout exceeded after 5m0s, init container init-setup: PodInitializing` |
| Init container terminated | `pod init container init-setup terminated: Error (exit code 1)` |
| Init container not ready | `Browser pod init container init-setup failed: ImagePullBackOff - back-off pulling image` |
| Container terminated | `pod container browser terminated: OOMKilled (exit code 137)` |
| Container not ready | `pod container browser failed: CrashLoopBackOff - back-off restarting failed container` |
| Pod failed | `pod has failed with reason: OOMKilled - container exceeded memory limit` |

This message is available in `Browser.status` until the CR is removed and is propagated as an SSE event by `browser-service`, making it observable to clients before the CR disappears.

---

## Configuration Flags

The controller binary accepts the following flags:

| Flag | Default | Description |
|---|---|---|
| `--metrics-addr` | `:8080` | Address the metrics endpoint binds to |
| `--health-probe-bind-address` | `:8081` | Address the health/readiness probe binds to |
| `--enable-leader-election` | `false` | Enable leader election for HA deployments |
| `--browser-pod-creation-timeout` | `5m` | How long the controller waits for a new Pod to leave `Pending` before force-deleting it and marking the `Browser` as `Failed` |
| `--browser-pod-deletion-timeout` | `5m` | How long the controller waits for a Pod to finish terminating before issuing a force-delete |
| `--browser-pending-timeout` | `0` (disabled) | How long a `Browser` may stay in `Pending` without an associated Pod before being marked `Failed / PendingTimeoutExceeded`. Useful when Pod creation is blocked by `ResourceQuota` and the backlog needs a deterministic cap. Set to `0` to disable. |
| `--max-retries` | `3` | Max retries for conflict resolution on Browser status updates |
| `--max-workers` | `4` | Max concurrent reconcile workers for the Browser controller |
| `--rate-limiter-base-delay` | `100ms` | Base delay for the exponential failure rate limiter |
| `--rate-limiter-max-delay` | `30s` | Max delay for the exponential failure rate limiter |

---

### `--browser-pending-timeout` in detail

When the cluster runs out of Pod quota (or another admission plugin blocks Pod creation), the controller keeps retrying and the `Browser` CR stays in `Pending` indefinitely. `--browser-pending-timeout` gives the controller a deterministic deadline: if a `Browser` CR has lived without a Pod for longer than the configured duration, the next reconcile marks it `Failed` with reason `PendingTimeoutExceeded` and message `Browser did not start within <timeout>`. On the following reconcile the standard failure path removes the CR.

The timeout is measured against `Browser.metadata.creationTimestamp`, not the time the controller first saw the CR, so a restart of the controller does not reset the window.

The default is `0`, which disables the timeout entirely and preserves the previous behavior of retrying forever.

---

## Build & Generate

This project uses `make` to generate code, manifests, and build the controller image.

### Install tools

```bash
make install-tools
```

### Generate code and manifests

```bash
make generate
make manifests
```

Or run everything:

```bash
make all
```

### Build and push image

```bash
make docker-build
make docker-push
```

Or combined:

```bash
make deploy
```

## Deployment

Helm chart [selenosis-deploy](https://github.com/alcounit/selenosis-deploy)
