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
- real A/B acceptance for Kubernetes readiness, Worker polling, Current routing, semantic projection, explicit historical pinned routing, immutable image identity, independent invocation IDs, downstream effects, and resource cleanup.
- `samples/hello`: minimal sequential typed Workflow, migrated off raw Temporal authoring;
- `samples/parallel-confirmation`: idle approval gate, authorized action, dynamic two-branch fork/join, Worker restart and action reconciliation;
- `samples/dynamic-decision`: recorded Activity result selects one branch, exposes the unselected candidate as `skipped`, and converges at finalize.
- all three Samples are self-contained Worker repositories with their own versioned Go module, Makefile, Docker context, build/push/kind scripts and value-first README; the publish request example is centralized under `docs/api`, and root targets only delegate;
- Sample repositories no longer contain a manifest generator, checked-in generated contract, control-plane publish body or redundant local runtime-config wrapper; SDK host-entrypoint simplification remains a separate follow-up;
- Tenant-derived Console HTTP/JSON API with stable errors, CSRF, request IDs, overview/quota read model, Worker/Version/Workflow/Run resources, async publish polling, revision-safe description updates, Current/historical Run starts, cancel, projection-constrained Gateway actions and action outcome polling;
- durable Tenant + principal scoped WorkerVersion publish idempotency with canonical payload hashing, same-operation replay, payload-conflict rejection, Audit references and terminal reservation retention;
- copy-on-write FileStore commits: persistence failure leaves both the process-visible snapshot and the on-disk snapshot unchanged across catalog, invocation, quota, action, publish, Audit and bootstrap records;
- atomic bootstrap acceptance: the read-only Worker contract, exact-retry receipt and accepted Audit are committed together, so a failed durable write leaves the pending WorkerVersion safely retryable;
- candidate-bound bootstrap workload identity: TokenReview Pod UID claims, live Pod UID/ServiceAccount, Tenant/Worker/version labels, an unpredictable rollout generation and the ReplicaSet→canonical Deployment owner chain must all match the credential binding before contract registration;
- durable bootstrap promotion: registration records a stable server-owned attempt, the controller resumes accepted pending WorkerVersions after restart, transient local persistence failures retry the same receipt/attempt, pinned probes attach to an existing same-attempt execution after an ambiguous start response, and each promotion phase is atomically committed with its Tenant-scoped Audit;
- race-free Current starts and durable Run lifecycle: default starts pin the server-resolved Current snapshot inside the same serialized transition used by promotion; `starting` Invocation plus quota is committed before Temporal, ambiguous starts attach/reconcile by deterministic Workflow ID, terminal state plus lease release is atomic, and the background reconciler restores pending starts and stale Tenant quota after restart without requiring a Run detail read;
- deterministic concurrent action routing and contract input enforcement: one Org SDK dispatcher routes the reserved Signal by stable runtime node ID with a bounded pre-wait inbox, while Workflow, Signal and Query JSON inputs are canonicalized and schema-validated before quota mutation or executor calls;
- complete bootstrap lifecycle Audit: credential issuance, known-credential receipt/verification/rejection/revocation and registration state commit atomically without credential material; promotion records durable poller-ready, probe-verified, retrying, failed-phase and Current-success outcomes using its stable attempt ID;
- Go server-rendered Console with progressive JavaScript, approved calm console visual language, read-only generated contract display, responsive resource tables, runtime dependency-driven DAG layout, equivalent mobile structured node list and persistent delivery-unknown action feedback;
- Workflow Trigger Console uses one bounded JSON/YAML payload editor rather than schema-derived fixed fields, keeps schema as a copyable read-only reference, and sends canonical JSON to the existing service validator; optional normalized Run description persists in Tenant-scoped Run list/detail/Audit data and participates in durable start idempotency intent;
- loopback-only local Console executable with server-configured development Tenant/principal identity; request headers cannot override Tenant.
- real local Console acceptance across `kind-org` and host Temporal: Hello covers Worker/Workflow plus Current and historical pinned Run reads; parallel-confirmation submits and retries the authorized action through the HTTP Gateway then polls accepted outcome; dynamic-decision verifies both runtime branches expose the unselected node as `skipped` through the Console Run API.

Still deferred:

- security measures proposed by the unapproved `010-workflow-execution-risk-defense.md` Draft, including adversarial-image isolation and production supply-chain policy.

Latest local verification on 2026-08-02:

- root `go test -race ./...` and `go vet ./...` passed;
- all three independent Sample modules passed `go test -race ./...` and `go vet ./...`;
- `make sdk-temporal-test` passed real Worker stop/Signal/restart/replay verification;
- `make e2e-local`, `make parallel-e2e-local`, and `make dynamic-e2e-local` passed against the running local Temporal service and `kind-org`;
- automatic cleanup removed each test platform Kubernetes Namespace and test-created image.
- copied-directory independence tests, real standalone Docker builds, registry-digest push contract tests, documentation links/terminology checks and Sample-directory-owned kind-load paths passed.
- Sample slimming acceptance passed after removing obsolete artifacts: centralized publish-contract validation, stale-path checks, all three copied modules, and all three real kind + Temporal paths remain green.
- publish idempotency HTTP/service/FileStore tests, CSRF documentation path, Sample revision commands and parallel required input passed; all three real kind + Temporal acceptance paths remained green.
- review-fixes Stage 1 fault injection passed for FileStore write failure, quota acquire/release/reconcile and bootstrap acceptance/restart exact retry; root and Sample race/vet/docs checks plus all three real kind + Temporal acceptance paths remained green.
- review-fixes Stage 2 identity tests reject missing/mismatched TokenReview Pod UID claims, stale rollout generations, mismatched Tenant/Worker/version labels and broken owner UIDs; real `kind-org` bootstrap verified the projected token claim and live owner chain.
- review-fixes Stage 3 fault injection passed for process restart from a reopened FileStore, final promotion-state persistence failure, atomic phase/Audit persistence and ambiguous probe start attachment; root and Sample race/vet/docs checks, SDK Temporal restart/replay and all three real kind + Temporal acceptance paths remained green.
- review-fixes Stage 4 tests cover concurrent default Start/promotion ordering, durable pre-Temporal reservation, ambiguous Temporal success followed by local commit failure, same-ID start attach, FileStore routing recovery, atomic Current catalog transition, atomic terminal/quota transition and background stale-lease reconciliation.
- review-fixes Stage 5 tests reverse-deliver actions to concurrent waiting nodes without loss/cross-routing and prove invalid Workflow, Signal and Query inputs make zero quota/executor mutations.
- review-fixes Stage 6 tests cover credential issuance/acceptance/rejection/expiry Audit without raw token or token hash, atomic FileStore issuance/rejection/acceptance retry, distinct image/identity failure classes, poller/probe outcome and promotion retry/failure-phase Audit, plus required Sample input and resource guidance; root/Sample race/vet/docs, SDK Temporal restart and all three real kind + Temporal acceptance paths passed.

The project does not claim exactly-once external effects. The safety contract requires downstream idempotency or an explicit reconciliation/compensation policy for ambiguous write outcomes.
