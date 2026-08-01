# Local backend development

> Terminology: this guide follows the canonical [org glossary](architecture/glossary.md). Product isolation is a Tenant; the configured infrastructure targets are the shared platform Temporal Namespace and platform Kubernetes Namespace.

The MVP development target is a local `kind` Kubernetes cluster plus a Temporal development server on the host. The control plane and Worker use distinct connection addresses because `localhost` inside a kind Pod refers to the Pod itself.

## Prerequisites

- Go 1.26+
- Docker
- `kubectl`
- `kind`
- Temporal CLI

The local acceptance environment is expected to provide the `org` kind cluster (`kind-org` context) and Temporal at `127.0.0.1:7233`. `make e2e-preflight` verifies their current state without creating or changing them.

## Start the dependencies

```sh
make check-tools
make kind-up
make temporal-dev
```

`make temporal-dev` starts the service at `127.0.0.1:7233`, its advanced diagnostics UI at `http://127.0.0.1:8080`, and persists development state in `.org/temporal.db`.

On Docker Desktop, kind Pods reach the host service through `host.docker.internal:7233`. On Linux, set `ORG_WORKER_TEMPORAL_ADDRESS` to an address reachable from kind nodes. Never set it to Pod-local `127.0.0.1`.

## Runtime configuration

| Variable | Development default | Purpose |
|---|---|---|
| `ORG_TEMPORAL_ADDRESS` | `127.0.0.1:7233` | Host-side control-plane connection |
| `ORG_WORKER_TEMPORAL_ADDRESS` | `host.docker.internal:7233` | Address injected into Worker Pods |
| `ORG_WORKER_BOOTSTRAP_ENDPOINT` | `http://host.docker.internal:8090/internal/v1/bootstrap/register` | Internal endpoint reachable from kind Pods; production uses TLS |
| `ORG_TEMPORAL_NAMESPACE` | `default` | Shared platform Temporal Namespace used internally |
| `ORG_TEMPORAL_WEB_URL` | `http://127.0.0.1:8080` | Advanced diagnostics deep-link base |
| `ORG_KUBE_CONTEXT` | `kind-org` | Configurable Kubernetes context |
| `ORG_KUBECONFIG` | empty | Optional kubeconfig path |
| `ORG_KUBE_NAMESPACE` | `org-workers` | Shared platform Kubernetes Namespace for Worker workloads |
| `ORG_REGISTRY_ALLOWLIST` | `ghcr.io` | Comma-separated accepted image registries |
| `ORG_STATE_FILE` | `.org/state.json` | Local audit projection store |
| `ORG_CONSOLE_ADDRESS` | `127.0.0.1:8090` | Loopback-only local Console listen address |
| `ORG_CONSOLE_TENANT_ID` | `tenant-local` | Server-configured local Tenant ID |
| `ORG_CONSOLE_TENANT_SLUG` | `local` | Server-configured local Tenant slug |
| `ORG_CONSOLE_TENANT_NAME` | `Local Development` | Tenant display name |
| `ORG_CONSOLE_PRINCIPAL_ID` | `local-developer` | Local authenticated principal |

Production supplies its own Kubernetes context, kubeconfig, Temporal endpoints, credentials environment, and registry allowlist.

## Start the Console

The bundled executable is deliberately a loopback-only development entrypoint. Tenant identity and permissions come from server configuration; request headers cannot select a Tenant. A production deployment must wire the `console.Authenticator` boundary to its real session and membership system.

```sh
ORG_REGISTRY_ALLOWLIST=org.local,ghcr.io make console-dev
```

Open `http://127.0.0.1:8090`. The sidebar contains only 总览, Workers, Workflows and Runs. A candidate Worker registers its Org SDK contract automatically during startup; Console only shows registration/probe status and the resulting read-only contract. Runtime DAGs come from validated semantic projection; node actions go through the Gateway action API.

## Image boundary

`org` does not build or publish Worker images. A publish request receives an already-published OCI reference pinned by `sha256` digest, version description, runtime references and source provenance. The deployed Org SDK registers the generated contract automatically; Console does not accept manifest uploads. Source repository fields are audit data only; the control plane does not clone them.

## Verification

```sh
make docs-test
make backend-test
make sample-test
make parallel-sample-test
make dynamic-sample-test
```

## Real local end-to-end acceptance

The control-plane acceptance test is opt-in because it builds images and creates real local resources. It uses the deliberately minimal `samples/hello` Worker as an external user fixture; the test itself belongs to `org` under `test/e2e`.

```sh
make e2e-preflight
make e2e-local
make parallel-e2e-local
make dynamic-e2e-local
```

`e2e-preflight` is read-only. It hard-fails unless Docker, `kind-org`, a Ready Kubernetes node, Temporal at `127.0.0.1:7233`, and all three Sample Dockerfiles are available. Generated manifest files are not a runtime prerequisite.

`e2e-local` uses Hello and then:

1. creates one unique platform Kubernetes Namespace, shared by both test Tenants, plus an idempotent downstream fixture;
2. builds and kind-loads sample versions A and B with immutable digest aliases;
3. submits the real metadata/provenance deployment requests through `ControlPlane`;
4. waits for Kubernetes readiness and Temporal Worker polling;
5. verifies an unqualified invocation uses Current version B;
6. verifies the semantic DAG projection and result;
7. verifies an explicit historical invocation uses pinned version A;
8. deletes the unique platform Kubernetes Namespace and test-created image tags/aliases.

`parallel-e2e-local` uses `samples/parallel-confirmation` and verifies idle `waiting-for-user`, Worker restart, a Gateway-authorized and idempotent `confirm` action, two runtime branches, join/finalize, action outcome reconciliation, and Tenant audit.

`dynamic-e2e-local` uses `samples/dynamic-decision` and runs both `concise` and `detailed` routes. Each Run executes only the selected Activity while retaining the unselected node as `skipped`, then completes their common finalize node.

The test prints `E2E RUN_ID=<id>` before mutation. Automatic cleanup runs on success and failure. If the process itself is interrupted before Go cleanup executes, recover with the exact printed identifier:

```sh
make e2e-clean RUN_ID=<id>
```

These targets never delete the `kind-org` cluster, host Temporal process, unrelated platform Kubernetes Namespace resources, or unrelated images. A normal `go test ./...` compiles but skips infrastructure tests; only the explicit E2E targets set `ORG_E2E=1`.

The real Hello acceptance now also traverses the Console HTTP read contract for Worker/Workflow, Current and explicit historical Run details, dynamic semantic projection, Tenant isolation, and the server-rendered Run shell.
