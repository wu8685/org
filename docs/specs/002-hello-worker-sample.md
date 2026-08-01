# Hello Worker sample

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product isolation is a Tenant; the underlying resources are the shared platform Temporal Namespace and platform Kubernetes Namespace.

> Approved amendment: `004-worker-identity-and-description.md` removes scope and duplicate Worker name from sample registration metadata.

## Status

**Approved — implementation authorized on 2026-08-01.**

This specification replaces the earlier order-fulfillment sample.

The order sample mixed the integration contract with inventory vocabulary, an HTTP downstream, a fixture server, secrets, and retry-failure plumbing. Those details obscured the user path that the sample is meant to teach. External-effect crash safety remains covered by `org` safety and E2E infrastructure where needed; it is not a reason to burden user-owned Worker code.

The replacement is intentionally small enough to read end to end: one Workflow, two sequential Activities, three projected steps, one Worker executable, one Dockerfile, and one build script. Two Activities are the minimum needed to make orchestration and data flow visible rather than presenting a degenerate one-node DAG.

## Goal

Provide an independent Go module at `samples/hello` that models the actual user path:

```text
write Worker -> test -> build image -> kind load -> register in org -> deploy -> invoke -> inspect result and projection
```

The sample does not import `org` internal packages. Temporal implementation details are retained only in its maintenance documentation and source code; the returned projection uses ordinary business-step language.

## Stable contract

| Field | Value |
|---|---|
| sample directory | `samples/hello` |
| Worker name | `hello-worker` |
| Workflow type | `HelloWorkflow` |
| Activity types | `PrepareGreeting`, then `ComposeGreeting` |
| projection query | `org_projection` |
| image repository | `org.local/hello-worker` |
| Task Queue | `TEMPORAL_TASK_QUEUE` |
| Worker Deployment | `TEMPORAL_WORKER_DEPLOYMENT` |
| Worker Build ID | `TEMPORAL_WORKER_BUILD_ID` |
| kind Pod Temporal address | `host.docker.internal:7233` |

`worker-metadata.json` is the canonical version contract and repeats neither Worker name nor scope. The E2E harness supplies `workerName` and version description in the WorkerVersion request.

## Input, output, and DAG

Input:

```json
{"name":"Codex"}
```

Output:

```json
{
  "message":"Hello, Codex!",
  "workerVersion":"2026.08.1",
  "idempotencyKey":"<sha256 hex>"
}
```

The DAG has exactly three semantic steps:

```text
prepare-greeting -> compose-greeting -> completed
```

`PrepareGreeting` trims and validates `name`, then returns a small `GreetingContext` containing the normalized name. `ComposeGreeting` consumes that context, constructs the final message, and returns the Worker version injected from Worker startup configuration. Both Activity results are recorded in Workflow history, so the data dependency is durable and current versus explicitly selected historical Worker versions can be distinguished by the final result.

The Workflow registers `org_projection` before scheduling the Activities. Its terminal value is:

```json
{
  "steps":[
    {"id":"prepare-greeting","label":"Prepare greeting","status":"completed"},
    {"id":"compose-greeting","label":"Compose greeting","status":"completed"},
    {"id":"completed","label":"Completed","status":"completed"}
  ],
  "currentStep":"completed",
  "status":"completed",
  "allowedActions":[]
}
```

## Temporal correctness and idempotency

- Workflow code only updates deterministic in-memory projection state and schedules the two Activities in order.
- `PrepareGreeting` owns validation and normalization; `ComposeGreeting` owns message composition and version output.
- Worker startup reads environment configuration and injects the version into `ComposeGreeting`; Workflow code never reads it.
- The Workflow assigns stable Activity IDs `prepare-greeting` and `compose-greeting`.
- `ComposeGreeting` derives `sha256(workflowID + NUL + activityID)` and returns it as a visible teaching aid.
- Neither Activity has an external side effect, so repeated attempts are naturally safe. A nearby comment explains that a real write Activity would pass this stable key to its downstream system.
- Retry policy is deliberately visible but small: 100 ms initial interval, maximum 2 s, maximum three attempts, five-second attempt timeout.

The sample does not contain an HTTP client, downstream fixture, inventory store, compensation policy, or crash simulator. Tests for actual ambiguous external writes belong in `internal/safety` or `test/e2e`, not in this teaching sample.

## Runtime configuration

Required environment:

- `TEMPORAL_ADDRESS`;
- `TEMPORAL_TASK_QUEUE`;
- `TEMPORAL_WORKER_DEPLOYMENT`;
- `TEMPORAL_WORKER_BUILD_ID`.

`TEMPORAL_NAMESPACE` defaults to `default`. Missing required configuration is a startup error.

The Worker opts into Worker Deployment Versioning using the injected deployment name and Build ID. `HelloWorkflow` is registered with pinned versioning behavior.

## Test-first acceptance

Before implementation, tests must describe:

1. `HelloWorkflow` schedules `PrepareGreeting` followed by `ComposeGreeting` and returns the greeting, version, and stable key;
2. the terminal `org_projection` contains exactly `prepare-greeting -> compose-greeting -> completed`;
3. `PrepareGreeting` trims a valid name into `GreetingContext` and rejects an empty one;
4. `ComposeGreeting` consumes the context and reports the configured Worker version;
5. the same Workflow and Activity identity derives the same key across attempts;
6. runtime configuration requires Worker identity and defaults the platform Temporal Namespace;
7. checked-in metadata matches the Workflow, two Activities, query, and three-step DAG;
8. the Dockerfile is non-root and traceably labeled;
9. build and kind-load scripts print the tag and an immutable, CRI-inspectable digest reference.

The formal control-plane E2E must build two image versions, deploy both through `org`, verify an unqualified invocation runs the current version, verify an explicit historical request runs the older version, and inspect the completed three-step projection.

## Image build and local kind path

From the sample directory:

```sh
./scripts/build-image.sh 2026.08.1 <7-to-64-character-hex-revision> --kind-load
```

Equivalent repository-root target:

```sh
make sample-kind-load SAMPLE_VERSION=2026.08.1 SAMPLE_COMMIT=<revision>
```

The traceable tag is:

```text
org.local/hello-worker:<version>-<revision>
```

The script prints:

```text
IMAGE_TAG=org.local/hello-worker:<version>-<revision>
IMAGE_DIGEST=org.local/hello-worker@sha256:<manifest-digest>
```

The build script only builds and optionally loads an image. Worker name, version description, runtime resources, and source provenance remain explicit registration-time fields in `deployment-request.example.json` and are not hidden in build logic.

## Non-goals

- demonstrating a realistic business domain;
- making network calls or external writes;
- embedding E2E fixtures in user sample code;
- implementing UI or control-plane APIs;
- publishing to a remote registry.
