# Browser Controller

Browser Controller is a Kubernetes controller that manages `Browser` and `BrowserConfig` custom resources for the Selenosis ecosystem. It creates and monitors browser pods based on `Browser` requests and a `BrowserConfig` template/override system.

## What it does
- Registers `Browser` and `BrowserConfig` CRDs.
- Reconciles `Browser` resources into Pods.
- Maintains `Browser.status` (pod IP, phase, container statuses, start time).
- Caches `BrowserConfig` data in an in-memory store for fast lookups during reconciliation.
- Exposes metrics and health/ready probes.

## Components
- `cmd/controller/main.go`: controller manager entrypoint, flags, logging, manager setup.
- `controllers/browser`: `BrowserReconciler` (pod creation, status updates, deletion handling).
- `controllers/browserconfig`: `BrowserConfigReconciler` (CRD wiring, store updates).
- `store/browserconfig_store.go`: in-memory cache of merged browser configs keyed by `namespace/browser:version`.
- `apis/browser/v1`: `Browser` CRD types.
- `apis/browserconfig/v1`: `BrowserConfig` CRD types and merge logic.

## CRDs

### Browser
Spec fields:
- `browserName` (string)
- `browserVersion` (string)

Status fields:
- `podIP`
- `phase`
- `message`, `reason`
- `startTime`
- `containerStatuses`

### BrowserConfig
- `template`: shared defaults (env, resources, sidecars, volumes, labels, annotations, security context, etc.).
- `browsers`: map of browser name -> version -> overrides.
- Merge logic combines `template` with per-version overrides to produce the effective pod spec.

## Reconciliation flow (high level)
1. A `Browser` resource is created.
2. The controller resolves the effective config from the `BrowserConfigStore`.
3. It creates or updates a Pod owned by the `Browser` resource.
4. Status is updated based on Pod phase, IP, and container state.
5. On deletion, the controller removes the pod and finalizer.

## Flags
The controller uses controller-runtime flags (see `cmd/controller/main.go`):
- `--metrics-addr` (default `:8080`)
- `--health-probe-bind-address` (default `:8081`)
- `--enable-leader-election` (default `false`)

## Build
```bash
go mod download
go build -o bin/manager ./cmd/manager
```

## Docker
The provided Dockerfile builds a distroless image:

```Dockerfile
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY bin/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
```

Build and run:
```bash
docker build -t browser-controller:local .
```

## Code generation
Makefile targets generate CRDs and clients:
- `make generate` (deepcopy, clientset, listers, informers)
- `make manifests` (CRD and RBAC manifests)
- `make docker-build` (runs `manifests`, `generate`, `tidy`, `fmt`, `vet`)

Tools installed by `make install-tools`:
- `deepcopy-gen`, `client-gen`, `lister-gen`, `informer-gen`, `controller-gen`

## Notes
- The controller is stateless and can be scaled horizontally.
- Leader election is supported via `--enable-leader-election`.
- RBAC manifests are generated into `config/rbac` by `make manifests`.
