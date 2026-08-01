# MVP implementation status

> Terminology: this status follows the canonical [org glossary](architecture/glossary.md). Product isolation is a Tenant; infrastructure uses one shared platform Temporal Namespace and one shared platform Kubernetes Namespace.

Implemented and covered by tests:

- immutable OCI digest and registry allowlist validation;
- Worker metadata contract validation, including Pinned workflows and semantic projections;
- write-Activity idempotency or reconciliation policy enforcement;
- crash-window safety test: external success followed by Worker crash and Activity retry produces one downstream effect when the stable idempotency key is honored;
- deployment orchestration across Kubernetes readiness, Temporal poller visibility, and Current-version promotion;
- Current and explicit historical-version Workflow starts, with a Temporal Pinned Versioning Override for the latter;
- independent invocation IDs plus optional idempotency-key deduplication;
- JSON-file audit projection persistence;
- declared Signal/Query enforcement, cancellation, and semantic invocation projection;
- constrained plain Kubernetes Deployment and ServiceAccount generation via configurable `kubectl` context;
- configurable host-side and kind-Pod Temporal endpoints;
- Temporal Go SDK adapter for Worker Deployment Versioning, execution operations, and diagnostics status.
- first-class Tenant → Worker → WorkerVersion → Workflow Run model with version-level required description, revision/If-Match updates, Tenant-qualified store/audit/projection data, and no public `scope` field;
- canonical Tenant + Worker routing for Task Queue, Worker Deployment, Workflow ID, Kubernetes Deployment and ServiceAccount names;
- Org SDK typed authoring/runtime adapter with generated JSON Schema, canonical manifest digest, contract probe, stable node/Activity identity, bounded dynamic graph projection, retry/side-effect policy, Activity hooks, and Pinned registration;
- Workflow-owned dynamic if/else, fan-out/join, `skipped` nodes, stable dependencies and runtime bounds; the control plane validates projection rather than reconstructing it from Temporal Event History;
- Workflow-internal durable `WaitForAction` / `AwaitConfirmation`, plus the control-plane action Gateway for Tenant authorization, schema validation, operation deduplication, delivery-unknown, Workflow outcome reconciliation and audit;
- opt-in real local control-plane E2E using Temporal at `127.0.0.1:7233`, Kubernetes context `kind-org`, and digest-pinned Org SDK Samples;
- real A/B acceptance for Kubernetes readiness, Worker polling, Current routing, semantic projection, explicit historical pinned routing, provenance, independent invocation IDs, downstream effects, and resource cleanup.
- `samples/hello`: minimal sequential typed Workflow, migrated off raw Temporal authoring;
- `samples/parallel-confirmation`: idle approval gate, authorized action, dynamic two-branch fork/join, Worker restart and action reconciliation;
- `samples/dynamic-decision`: recorded Activity result selects one branch, exposes the unselected candidate as `skipped`, and converges at finalize.
- all three Samples are self-contained Worker repositories with their own versioned Go module, Makefile, Docker context, build/push/kind scripts and value-first README; the publish request example is centralized under `docs/api`, and root targets only delegate;
- Sample repositories no longer contain a manifest generator, checked-in generated contract, control-plane publish body or redundant local runtime-config wrapper; SDK host-entrypoint simplification remains a separate follow-up;
- Tenant-derived Console HTTP/JSON API with stable errors, CSRF, request IDs, overview/quota read model, Worker/Version/Workflow/Run resources, async publish polling, revision-safe description updates, Current/historical Run starts, cancel, projection-constrained Gateway actions and action outcome polling;
- durable Tenant + principal scoped WorkerVersion publish idempotency with canonical payload hashing, same-operation replay, payload-conflict rejection, Audit references and terminal reservation retention;
- Go server-rendered Console with progressive JavaScript, approved calm console visual language, read-only generated contract display, responsive resource tables, runtime dependency-driven DAG layout, equivalent mobile structured node list and persistent delivery-unknown action feedback;
- loopback-only local Console executable with server-configured development Tenant/principal identity; request headers cannot override Tenant.
- real local Console acceptance across `kind-org` and host Temporal: Hello covers Worker/Workflow plus Current and historical pinned Run reads; parallel-confirmation submits and retries the authorized action through the HTTP Gateway then polls accepted outcome; dynamic-decision verifies both runtime branches expose the unselected node as `skipped` through the Console Run API.

Still deferred:

- security measures proposed by the unapproved `010-workflow-execution-risk-defense.md` Draft, including adversarial-image isolation and production supply-chain policy.

Latest local verification on 2026-08-01:

- root `go test -race ./...` and `go vet ./...` passed;
- all three independent Sample modules passed `go test -race ./...` and `go vet ./...`;
- `make sdk-temporal-test` passed real Worker stop/Signal/restart/replay verification;
- `make e2e-local`, `make parallel-e2e-local`, and `make dynamic-e2e-local` passed against the running local Temporal service and `kind-org`;
- automatic cleanup removed each test platform Kubernetes Namespace and test-created image.
- copied-directory independence tests, real standalone Docker builds, registry-digest push contract tests, documentation links/terminology checks and Sample-directory-owned kind-load paths passed.
- Sample slimming acceptance passed after removing obsolete artifacts: centralized publish-contract validation, stale-path checks, all three copied modules, and all three real kind + Temporal paths remain green.
- publish idempotency HTTP/service/FileStore tests, CSRF documentation path, Sample revision commands and parallel required input passed; all three real kind + Temporal acceptance paths remained green.

The project does not claim exactly-once external effects. The safety contract requires downstream idempotency or an explicit reconciliation/compensation policy for ambiguous write outcomes.
