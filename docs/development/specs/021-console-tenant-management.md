# Console Tenant management

> Terminology follows the canonical [org glossary](../../user/architecture/glossary.md). A Tenant is the product authorization and data boundary. It is never mapped to, or presented as, a platform Temporal Namespace or platform Kubernetes Namespace.

## 状态

**Approved — implementation authorized by the user on 2026-08-02.**

本规格在 [`019-console-run-list-semantic-status.md`](019-console-run-list-semantic-status.md) 与 [`020-safe-run-failure-information.md`](020-safe-run-failure-information.md) 完成后独立实施，是 [`003-multi-tenant-shared-infrastructure.md`](003-multi-tenant-shared-infrastructure.md)、[`011-console-ui-http-api.md`](011-console-ui-http-api.md) 与 [`018-console-tenant-context-selection.md`](018-console-tenant-context-selection.md) 的 Tenant management amendment。不实施 [`010-workflow-execution-risk-defense.md`](010-workflow-execution-risk-defense.md) 中尚未批准的新增策略。

## 用户结果与边界

Console 顶栏增加“管理 Tenants”入口。一个 authenticated principal 只能枚举其真实 membership 所授权的 Tenant；每个 Tenant detail 显示 immutable stable slug、display name、description、active 状态、quota usage/limits，以及成员的 principal、role 与 server-derived permissions。

MVP 提供：

- `tenant:read`：读取该 principal 已加入的 Tenant；
- `tenant:create`：创建 active Tenant，creator 自动成为 owner，并可立即选择该 Tenant；
- `tenant:update`：更新 display name、description 与 quota policy；
- `tenant:member:manage`：把既有 platform principal 加入 Tenant、修改其 role、移除成员。

MVP 不提供 Tenant delete/hard delete，也不提供 suspend/resume。后者必须先定义对已部署 Worker、running/idle Run、action 与 quota lease 的影响，不能只切换一个状态字段。

当前 executable 仍是 loopback local-development auth，不伪造外部 identity provider：它只有 server-configured platform principal catalog，默认只含 `local-developer`；可选 `ORG_CONSOLE_PRINCIPALS` 仅声明本地测试中“已存在”的 principal ID/display name。管理 API 不能创建 principal、发送邮件或声称完成外部邀请。Production 必须让相同的 `PrincipalDirectory`/session boundary 接入真实身份与 membership source。

## Domain contract

### Tenant

```text
Tenant {
  id            server-generated opaque stable ID
  slug          immutable lower-case DNS label, 1..40 chars
  displayName   mutable plain text, 1..120 Unicode code points, one line
  description   mutable plain text, 0..500 Unicode code points, at most 10 lines
  status        active in this MVP
  quotaPolicy   finite positive limits
  revision      monotonic int64, begins at 1
  createdAt/updatedAt
}
```

Slug collision returns `409 tenant_slug_conflict` without revealing any Tenant the principal could not otherwise enumerate. Tenant ID、status、revision 与 timestamps are server-owned. PATCH cannot alter slug or status.

Quota update cannot set a limit below current durable usage/leases; rejection is `409 quota_below_current_usage`. This slice changes admission policy only; it does not evict existing Worker Pods or cancel Runs.

### Membership and role

```text
TenantMember {
  tenantId
  principalId
  principalDisplayName   copied from trusted PrincipalDirectory for read stability
  role                   owner | admin | operator | viewer
  revision
  createdAt/updatedAt
}
```

Permissions are derived by the server and are never accepted as request data:

| role | permissions |
|---|---|
| owner | `tenant:read`, `tenant:update`, `tenant:member:manage`, `tenant:create`, all existing Worker/Run/Audit/diagnostics permissions |
| admin | `tenant:read`, `tenant:update`, `tenant:member:manage`, all existing Worker/Run/Audit/diagnostics permissions; no `tenant:create` |
| operator | `tenant:read`, existing Worker/Run mutation and read permissions; no Tenant/member administration |
| viewer | `tenant:read`, Worker/Version/Workflow/Run reads only |

At least one owner must remain. Removing/demoting the last owner returns `409 last_tenant_owner`. Adding an existing member with the same role is idempotent; a conflicting role uses the revision-protected role PATCH. Unknown principal returns `404 principal_not_found`; response does not enumerate the principal catalog.

## Trust and authorization flow

Tenant/resource identity is never taken from a request body field named `tenantId`。Tenant routes use stable slug only as a locator:

1. authenticator establishes principal/session;
2. control plane resolves the slug server-side;
3. durable membership lookup verifies that principal belongs to the target Tenant and derives target membership permissions;
4. unauthorized/missing target returns the same `404 not_found` for reads and target mutations;
5. audit is written under the real target Tenant for allowed mutations and under the caller's current Tenant for authorization denial, without recording forged request identity.

Business API remains active-Tenant scoped through `AuthenticatedContext`。Tenant management does not add `tenantId` query/body controls to Worker/Version/Workflow/Run APIs.

## Durable storage and session refresh

Tenant, membership, mutation Audit, revision and local session selection are FileStore durable data. Create Tenant + creator membership + Audit is one copy-on-write persistence commit. Tenant update/member add-role-remove + Audit is likewise atomic; a persistence failure leaves live and disk snapshots unchanged.

`SessionAuthenticator` reads authorized memberships from a server-side `MembershipDirectory` on every authentication/selection boundary rather than retaining an immutable startup map. Therefore create/add/remove/update are visible without process restart. Selected Tenant remains durable by opaque session key. If the selected membership disappears, authentication safely falls back to the configured Local Development Tenant when still authorized, otherwise to the first authorized membership; if none remain, authentication fails closed.

The Local Development bootstrap Tenant is always initialized and its initial local creator membership is idempotently bootstrapped. Tenant management never deletes or renames it.

## HTTP/JSON API

All mutations require authenticated session、CSRF and JSON content type. Unknown fields are rejected.

```text
GET    /api/v1/tenants
POST   /api/v1/tenants
GET    /api/v1/tenants/{tenantSlug}
PATCH  /api/v1/tenants/{tenantSlug}                If-Match: "tenant-r<N>"
POST   /api/v1/tenants/{tenantSlug}/members
PATCH  /api/v1/tenants/{tenantSlug}/members/{principalId}  If-Match: "member-r<N>"
DELETE /api/v1/tenants/{tenantSlug}/members/{principalId}  If-Match: "member-r<N>"
```

Create body only accepts `slug`、`displayName`、optional `description`。PATCH accepts the full new `displayName`、`description` and `quotaPolicy` to avoid ambiguous partial nested quota merges. Member create accepts `principalId` and `role`; role PATCH only accepts `role`.

List/detail include usage and limits; detail includes members only when caller has `tenant:member:manage`, otherwise returns the caller's own membership only. ETag covers Tenant revision plus member revisions and quota usage so polling does not serve stale administration state.

`GET /api/v1/session` continues to expose only authorized Tenant selector items. After successful create, Console invokes the server-side selection operation and redirects to the new Tenant management detail. After self-removal, selector refresh chooses a safe authorized fallback.

## Console UI

- topbar has “管理 Tenants”; current Tenant identity remains visible everywhere;
- `/tenants` provides accessible desktop/mobile cards or table with display name、slug、status、role、quota summary；empty/error/loading states are explicit;
- `/tenants/new` or the shared accessible modal collects slug、display name、description；no browser prompt/confirm；
- `/tenants/{slug}` displays overview、quota usage/limits and member list；update/member controls are rendered only when the server read model says the operation is allowed；
- destructive member removal uses the existing Console modal pattern, names the principal/Tenant, and remains CSRF + If-Match protected；
- status/role/permission meaning is text, not color-only；ARIA labels/live error regions and mobile structured layout are required；
- no UI text mentions or displays platform Temporal/Kubernetes Namespace, raw credentials or technical routing.

## Audit

Durable Audit action names: `tenant.create`、`tenant.update`、`tenant.member.add`、`tenant.member.role.update`、`tenant.member.remove`。Records include target Tenant ID, acting principal, affected principal ID/role where applicable, authorization result, outcome and request ID. Description、credentials and full permission maps are not copied into Audit references.

## TDD and acceptance

1. domain red tests: validation, immutable slug, description/Unicode bounds, revision, roles and server-derived permissions。
2. Store red tests: atomic create/update/member mutations with Audit; duplicate slug; persistence/restart; last-owner invariant; failure injection leaves memory/disk unchanged。
3. service red tests: authorized list/create/update/member lifecycle, existing-principal check, quota-below-usage, permission denial/IDOR indistinguishable from missing, two principals/two Tenants and Audit。
4. auth red tests: dynamic membership refresh, selector update after create/remove, unauthorized Tenant not enumerable/selectable, safe restart/fallback。
5. API/UI red tests: CSRF、strict body、ETag/If-Match、tenant-scoped read models、no arbitrary `tenantId`、ARIA/mobile/modal/error states。
6. local browser smoke: create a second Tenant, automatic selection, update it, inspect quota/member data, return to Local Development; attempt unknown slug/principal and verify no enumeration。
7. root test/race/vet/docs and existing real kind+Temporal paths must remain green。This independent milestone is committed and pushed only after full verification.
