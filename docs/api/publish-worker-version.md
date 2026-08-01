# Publish a WorkerVersion

Publishing connects an immutable Worker image to an existing logical Worker. The candidate starts, its Org SDK registers the read-only Workflow contract automatically, and org promotes the Version only after deployment, registration, polling and probe checks pass.

> Product terminology follows the canonical [org glossary](../architecture/glossary.md). Tenant comes from the authenticated principal; it is never accepted from this request.

## Request

```http
POST /api/v1/workers/{workerName}/versions
Idempotency-Key: publish-2026-08-1
Content-Type: application/json
```

Use [`examples/publish-worker-version.json`](examples/publish-worker-version.json) as the body shape. Replace its image with the exact `IMAGE_DIGEST` returned by your registry push or Sample `make kind-load` command.

| Field | Meaning |
|---|---|
| `version` | Stable public Version label |
| `description` | Human-readable explanation of this Version |
| `image` | Immutable `registry/repository@sha256:<64 lowercase hex>` |
| `versionConfig` | Business configuration for this Version; never platform routing |
| `runtime` | Pod CPU/memory and Secret references |
| `source` | Repository, branch, commit and CI provenance |

The Worker name comes from the URL. Tenant comes from authentication. Do not send Tenant identity, Worker name, `scope`, platform routing, credentials, contract, metadata or projection fields in the body.

Mutable tags and `tag@digest` are rejected. org does not build or push the image for you.

## Response and verification

The API returns `202 Accepted` with a pollable operation URL. The Version remains pending while org verifies:

```text
candidate deployment
→ SDK automatic registration
→ Worker polling
→ pinned contract probe
→ Ready / Current
```

The registered contract is read-only in Console/API. Fix an invalid image or contract by publishing a new Version; do not try to overwrite an existing Version.

## Secrets and external effects

`runtime.environment` contains Secret references, not Secret values. Do not place credentials or sensitive input in Version configuration, Workflow history, projection, logs or Audit.

Workflow code must not perform external I/O. A write Activity must propagate a stable idempotency key or declare reconciliation/compensation behavior; retries do not guarantee external effects exactly once.
