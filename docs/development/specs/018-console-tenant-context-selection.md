# Console Tenant context 与 session selection

> Terminology: this specification follows the canonical [org glossary](../../user/architecture/glossary.md). Console 产品层只使用 Tenant；一个共享 platform Temporal Namespace 与一个共享 platform Kubernetes Namespace 不得出现在 Tenant selector、breadcrumb 或普通资源 URL 中，也不得暗示 Tenant 映射为底层 Namespace。

## 状态

**Approved — implementation authorized by the user on 2026-08-02.**

本规格是 [`011-console-ui-http-api.md`](011-console-ui-http-api.md) 的独立 Console amendment。它不改变 003 的共享基础设施模型，也不把 `tenantId` 加回 Worker、Version、Workflow、Run 或 action 请求。

## 目标

- Console 全局区明确展示当前 authenticated Tenant 的 display name 与 stable identifier。
- overview、Worker、Version、Workflow、Run、empty state 与 breadcrumb 都明确处于当前 Tenant context。
- principal 只能看到其 auth layer 返回的 Tenant memberships，并可在这些 membership 之间切换当前 session Tenant。
- 切换后所有 read、publish、start、cancel 与 action 请求继续使用服务端认证得到的 `AuthenticatedContext`；资源 URL 和 body 不携带 Tenant locator。
- local developer 默认仍只有 `Local Development` Tenant；可通过明确的 server-side local config 添加第二个 membership，供双 Tenant 开发验收。

## 信任边界与模型

Auth adapter 为每个已认证 session 提供：

```text
principal
session key
authorized Tenant memberships[]
selected Tenant
per-membership permissions
CSRF token
```

每个 membership 包含内部 Tenant ID、稳定 slug、display name 与 permissions。普通 session read model 可显示当前 Tenant ID/slug；selector 只提交 `tenantSlug` 到专用 session mutation，不接受任意 `tenantId`，也不把 Tenant 放进资源 URL。

`POST /api/v1/session/tenant`：

```json
{ "tenantSlug": "team-b" }
```

- 请求必须 authenticated、same-origin 且通过 CSRF；unknown 或 unauthorized slug 一律 `403 permission_denied`，不得泄露它是否真实存在。
- auth adapter 先在当前 session 的 membership catalog 中解析 slug，再持久化 selected internal Tenant ID；客户端不能提供 permissions、display name 或 Tenant ID。
- 成功返回更新后的 session read model 与 `redirect: "/"`。progressive JavaScript 随后导航到 `/`，避免把旧 Tenant 的 resource path 当成新 Tenant 的同名对象继续展示。
- session selection mutation 不创建/修改 Tenant，不改变 membership，也不授权此前无权访问的 Tenant。

## 持久化与 local developer

selected Tenant 按 opaque server-owned session key 持久化。刷新页面和重启 Console 后仍选择同一 authorized membership；若 membership 被移除或 Tenant 不可用，auth adapter fail closed 回到 server-configured default authorized Tenant，不接受旧客户端值。

local developer auth 使用固定 local session key、server-configured memberships 与同一组 development permissions。默认配置仍创建并选择：

```text
tenant-local / local / Local Development
```

可选 `ORG_CONSOLE_TENANTS` 使用 server-side JSON array 配置额外 local memberships；它只用于 loopback developer Console。production auth 必须从身份系统取得 principal memberships 与真实 session key，不能把 local config 当成生产授权源。

## HTTP 与 UI

`GET /api/v1/session` 返回：

- principal ID、CSRF token、当前 Tenant 与当前 membership permissions；
- `authorizedTenants` 仅含该 principal/session 的 memberships，按 display name/slug 稳定排序；
- 不返回其他 Tenant、platform credential、Task Queue 或任何 platform Namespace locator。

Console shell：

- topbar 始终展示 `Tenant`、display name、stable ID/slug；多个 membership 时显示 label 明确、键盘可用的 selector，单 membership 时仍显示静态 identity；
- selector mutation 的 pending/error 使用 `aria-live`，失败时保留当前 Tenant；移动端不得隐藏当前 Tenant identity；
- page heading 使用语义化 breadcrumb：当前 Tenant → resource hierarchy。资源 URL 仍是 `/workers/...`、`/workflows`、`/runs/...`，其 Tenant context 来自 session；
- overview 明确列出当前 Tenant status 与 quota usage；resource empty state 使用“当前 Tenant”文案；任何位置都不显示裸 `Namespace`。

## Tenant-scoped routing

每次 request 都重新调用 Authenticator，得到当前 selected Tenant 的 `AuthenticatedContext`。后台 publish goroutine复制该 request 已解析的 Tenant context；切换 session 不得把已受理 operation 改绑到另一个 Tenant。operation/run/action locator lookup 仍在该 request Tenant 内执行，cross-Tenant locator 返回 `404`。

两个 Tenant 可拥有同名 Worker、Version 与 Workflow。切换前后的 list/detail/start/action 必须分别命中各自 Tenant 数据，不得依赖 browser cache、query `tenantId` 或 path prefix 区分。

## TDD 与验收

1. session API 只列 authorized memberships；未授权 slug 无法枚举或选择。
2. selection mutation 要求 CSRF，忽略/拒绝 request header、query/body 中伪造的 `tenantId`。
3. refresh 与重新创建 authenticator/server 后从 durable session selection 恢复；membership 移除时安全回到 default。
4. 两 Tenant 同名 Worker 的 list/detail 不串数据；切换后 start/action 收到新 Tenant auth，切回后恢复原 Tenant。
5. all resource mutations 继续拒绝 `tenantId` 与 `scope` unknown fields。
6. topbar、breadcrumb、overview、empty state、ARIA 与 `<=700px` mobile CSS 有 contract tests；UI 不出现 platform Namespace 文案。
7. 使用两个 authorized local Tenant 做真实 HTTP/browser smoke：A/B 同名 Worker 分别可见，切换后 session、overview 与资源数据同步变化；unauthorized selection 返回 403。
8. root tests、race、vet、docs tests 与现有 local E2E 不退化；作为独立 milestone commit/push。
