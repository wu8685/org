# org Worker Hosting MVP

> Terminology: this specification follows the canonical [org glossary](../../user/architecture/glossary.md). Product isolation is a Tenant; the underlying resources are the shared platform Temporal Namespace and platform Kubernetes Namespace.

> Approved amendment: `004-worker-identity-and-description.md` removes public `scope` in favor of authenticated Tenant + Worker name and adds per-WorkerVersion description.

## Status

**Historical MVP baseline — amended by later approved specifications.**

## Goal

`org` is a Go control plane with a browser-based operations console. It accepts a published, immutable Worker image and its metadata, deploys it to Kubernetes, and exposes both service APIs and a UI for invoking and operating that Worker's Temporal Workflows.

The user-facing product is a DAG-like workflow experience. Temporal is an internal durable-execution substrate, not a concept that ordinary users must learn.

## MVP boundaries

- One tenant and one Kubernetes cluster in the first release.
- The `org` control plane is implemented in Go.
- Go Workers only.
- One WorkerVersion deployment per `{authenticated tenant, workerName, version}`.
- `{tenant, workerName}` deterministically selects the Temporal Task Queue and Temporal Worker Deployment.
- Multiple Worker versions may run concurrently on the Worker Task Queue through Temporal Worker Versioning.
- `org` maps each user-facing Worker version to a Temporal Worker Deployment Version; the immutable image digest is the release's verifiable artifact identity.
- `org` owns deployment state and the invocation API; the user's existing CI/CD pipeline owns source checkout, build, test, and image publication.
- The UI is the default user-facing path for deployment and operations; APIs remain available for programmatic callers.
- The default UI uses user-domain names, states, steps, and actions. It does not expose Temporal namespaces, Task Queues, Event History, replay, or retry internals.
- Temporal owns durable Workflow execution, history, retries, Signals, Queries, and cancellation.

## Inputs

Deployment request:

- `workerName`: tenant-local logical Worker and stable Task Queue boundary.
- `image`: immutable OCI image digest to deploy.
- `version`: human-facing Worker release version.
- `workerVersion`: the target WorkerVersion for a new Workflow Run; omitted means the Worker's Current version. An explicit value selects a Temporal Worker Deployment Version.
- `worker`: declared Worker name.
- `workflow`: approved Workflow type names and input schemas.
- `runtime`: CPU, memory, environment references, and service account reference.
- `source`: repository URL, branch, commit SHA, and CI run reference for provenance only.

Worker metadata contract:

- Metadata declares registered Workflows, Activities, DAG-step display data, input/output schemas, and each write Activity's idempotency-key contract and retry policy. It repeats neither Worker name nor scope.
- The image must run as a non-root user and expose the declared Worker behavior.
- Workflow logic must be deterministic; external I/O belongs in Activities.

## Required behavior

1. Validate the request, image digest, and Worker metadata before deployment.
2. Record the supplied image digest and source provenance as the deployment's immutable identity.
3. Do not clone repositories, run user builds, or publish images.
4. Create a Kubernetes Deployment and ServiceAccount with constrained resource and security settings.
5. Configure the Worker with the shared platform Temporal Namespace, server-derived tenant+worker Task Queue, and Temporal endpoint credentials.
6. Enable Temporal Worker Versioning and register the Worker with the Deployment Version mapped from its user-facing version.
7. Wait for the Worker to poll successfully before reporting the deployment ready.
8. Route invocation requests only to Workflow types declared by the deployed metadata.
9. Support starting a new Workflow against either the Worker's Current version or an explicitly selected ready version.
10. Use a Temporal Versioning Override for an explicitly selected Worker version.
11. Default long-running Workflows to Temporal's Pinned behavior, so an execution and its compatible Activities remain on the version selected at start.
12. Return a stable invocation identifier and support status, result, Signal, Query, and cancellation operations.
13. Preserve WorkerVersion and invocation audit records, including source provenance, image digest, actor, workerName, selected Worker version, Task Queue, and Temporal Workflow ID.
14. Provide a UI where an authorized user can:
    - create a WorkerVersion from Worker name, version description, image, metadata, version, and runtime inputs;
    - inspect Kubernetes readiness, Worker polling, image provenance, and deployment history;
    - start an approved Workflow and inspect its input, state, result, and Temporal link;
    - send an approved Signal, run an approved Query, or request cancellation;
    - view immutable source, image, actor, and audit metadata.
15. For each execution, provide a direct, authenticated link to the corresponding Temporal Web UI execution page. `org` does not duplicate Temporal's raw Event History, event timeline, or debugger views in the MVP.
16. Show a lightweight execution projection for ordinary users: declared DAG steps, current step, semantic status, blocking reason, and permitted next actions. The projection must be sourced from the Worker contract and Workflow query state, not inferred from raw Temporal Event History.

## Non-goals

- Arbitrary user containers or arbitrary shell execution.
- Cloning user repositories, building Worker source, or publishing Worker images.
- Multiple clusters, autoscaling policy, billing, and self-service RBAC.
- Live migration of a Pinned Workflow without an explicit authorized operation.
- Replacing Temporal Web UI or Temporal operational tooling.
- A visual Workflow authoring system; Workflows remain repository-defined code.
- A custom technical execution timeline or DAG renderer in the MVP. If a product need emerges for a business-step view, it must be a separately specified projection derived from declared Worker metadata rather than an interpretation of raw Temporal history.
- Teaching or requiring ordinary users to operate Temporal directly.

## Safety and isolation

- Never share a Task Queue across different `{tenant, workerName}` identities.
- Worker credentials are least-privilege and delivered through Kubernetes Secrets or workload identity, never repository contents.
- Image registries are allowlisted; deployed images must be referenced by digest.
- Runtime network access is policy-controlled.
- Failed or deleted deployments must not silently leave runnable Workflows without an explicit lifecycle policy.
- A Worker version with open Pinned Workflows must remain runnable. `org` may retire it only after all such executions have closed or an authorized migration policy has completed.
- Workflow code must not perform external I/O or other side effects. External actions run only in Activities.
- Every write Activity must propagate a stable idempotency key derived from the Workflow and Activity identity to its downstream system, or declare an explicit reconciliation and compensation policy.
- A non-idempotent Activity must not receive automatic retries without an approved exception policy; disabling retries alone does not remove the need to reconcile an ambiguous outcome.

## Acceptance scenarios

1. A valid image digest and Worker metadata produce a healthy Kubernetes Worker and a successful Workflow invocation through `org`.
2. A request without valid Worker metadata is rejected before deployment.
3. A Worker that cannot poll Temporal never becomes ready.
4. A call to an undeclared Workflow type is rejected by `org`.
5. An Activity failure follows its configured retry policy; a Workflow remains queryable and recoverable.
6. The recorded source provenance, image digest, Kubernetes workload, workerName, and Temporal Workflow ID can be correlated from a single invocation.
7. An authorized user can complete deployment and an approved Workflow invocation through the UI without using the CLI.
8. An ordinary user can identify the current declared step, blocking reason, and allowed next action without seeing Temporal-specific terminology.
9. A user can start a new Workflow on a selected past ready version while the Worker's newer Current version remains available for other invocations.
10. A long-running Workflow started on version A remains executable on Worker Deployment Version A after version B becomes Current.
11. If a Worker crashes after a write Activity has performed its external effect but before Temporal records completion, the retry does not create a duplicate effect.

## Local end-to-end acceptance

The control-plane repository owns an opt-in, real local acceptance test. The `samples/hello` project is only its user-owned Worker fixture and image source; the sample does not implement or drive control-plane acceptance.

### Fixed environment

- Temporal frontend: `127.0.0.1:7233`.
- Kubernetes context: `kind-org`.
- Worker Temporal address inside kind: `host.docker.internal:7233`.
- Worker fixture: `samples/hello`, using its checked-in metadata and kind-loaded digest reference.
- The test creates one unique platform Kubernetes Namespace, the same Worker name in two explicit Tenants, canonical Workflow IDs, and version identifiers for every run. Both Tenants deploy into that same platform Kubernetes Namespace.

The normal unit-test command remains independent of local infrastructure. The real test is explicitly enabled by `ORG_E2E=1`; when enabled, a missing CLI, unavailable Docker daemon, missing `kind-org`, closed Temporal endpoint, missing sample contract, image-build failure, or runtime assertion failure is a hard test failure rather than a skip. A dedicated Makefile target sets this opt-in flag and performs preflight checks.

### Required real path

1. Verify Docker, `kind`, `kubectl`, `kind-org`, and `127.0.0.1:7233` before mutating state.
2. Build and kind-load two traceable sample images, version A and version B, and resolve the immutable digest reference used by Kubernetes. This is test-fixture preparation, not a control-plane image-build feature.
3. Create two explicit Tenant records with finite quota policies and authenticated contexts derived by the harness; neither deployment nor Run DTO contains `tenantId`.
4. Read the sample's canonical Worker metadata and submit it under each authenticated Tenant's same Worker name with version description, digest, runtime, and source provenance.
5. Deploy version A through the real `kubectl` adapter; require Kubernetes readiness and an observed Temporal Worker Deployment poller before the deployment can become ready.
6. Deploy version B as Current and start an invocation without `workflowVersion`; require successful completion on version B.
7. Read the `org_projection` state through the normal control-plane projection method and require the declared semantic steps, terminal current step, semantic status, no block reason, and terminal allowed actions. Raw Temporal Event History must not be used to derive this assertion.
8. Start another invocation with explicit historical version A; require a Temporal Pinned Versioning Override and prove from both the audit record and Workflow result that version A executed while B remained Current.
9. Deploy the same Worker/version for Tenant B into the same platform Kubernetes Namespace; require different Task Queue, Worker Deployment, Workflow ID, Kubernetes Deployment, and ServiceAccount names.
10. Run Tenant B's Workflow successfully and prove Tenant A receives the same `not_found` result for Tenant B's opaque Run ID as for a missing Run; verify deployment and Audit lookups remain tenant-qualified.
11. Require an independent canonical Workflow ID for each invocation. The crash-after-external-success idempotency proof remains in the deterministic safety test, where the external effect can be counted exactly without weakening the real infrastructure assertions.

### Cleanup

Cleanup is registered before the first mutation and runs on success or failure. It stops the downstream fixture and deletes only the unique platform Kubernetes Namespace and images/tags created by this test. Workflow Executions are allowed to complete before teardown. Temporal records use unique identifiers in the existing shared platform Temporal Namespace; the harness neither creates nor deletes that platform Temporal Namespace. Cleanup failures are reported as test failures and never delete `kind-org`, the host Temporal process, unrelated platform Kubernetes Namespaces, or user images.

### Test location and command

- Test package: `test/e2e`.
- Dedicated command: `make e2e-local`.
- Preflight-only command: `make e2e-preflight`.
- Cleanup-only recovery command: `make e2e-clean` with the explicit run identifier printed by the test.

## Confirmed MVP decisions

1. Temporal endpoint is configurable. Local development defaults to a local Temporal service; production may use a self-hosted service or Temporal Cloud without changing the domain model.
2. Kubernetes uses plain Deployments and ServiceAccounts. Local development targets a `kind` cluster. The kubeconfig and context remain configurable for production.
3. Source repository data is provenance only. `org` does not fetch source and does not restrict provenance to a single forge; image registries are independently allowlisted.
4. The external service surface is HTTP/JSON plus the browser UI. gRPC is not part of the MVP.
5. The MVP persists deployment, invocation, and audit projections in a local JSON state file. Temporal and Kubernetes remain the authorities for execution and workload health.
