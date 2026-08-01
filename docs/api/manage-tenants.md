# 管理 Tenants

Console 的“管理 Tenants”入口用于查看和维护当前 principal 已获授权的 Tenant。它不会列出无权访问的 Tenant，也不会把产品 Tenant 映射为底层基础设施 Namespace。

## 当前边界

- `GET /api/v1/tenants` 只返回当前 principal 拥有 `tenant:read` 的 Tenant。
- `tenant:create` 可创建 Tenant；slug 是 immutable slug，创建后不能修改。creator 自动成为 owner，Console 随后选择新 Tenant。
- `tenant:update` 可修改 display name、description 和 quota policy，但 quota 不能低于当前已占用资源。
- `tenant:member:manage` 可添加既有 platform principal、修改 role 或移除成员。权限由服务端根据 `owner`、`admin`、`operator`、`viewer` role 派生，request 不能自行声明 permissions。
- 系统保护最后一个 owner，不能移除或降级最后一个 owner。
- MVP 不提供删除 Tenant，也不提供 suspend/resume。

本地 executable 的身份目录由服务端 `ORG_CONSOLE_PRINCIPALS` 配置，只模拟既有 platform principal catalog；它不创建外部身份、不发送邀请。默认只包含 `Local Development` principal。Production 应在相同的认证与 principal-directory 边界接入真实身份系统。

## Session、CSRF 与并发修改

先在保持同一认证 session 的情况下读取 session：

```http
GET /api/v1/session
```

所有写操作都要求该响应中的 `csrfToken`：

```http
X-CSRF-Token: <csrfToken>
Content-Type: application/json
```

更新 Tenant 或成员还必须使用最新读取响应的 ETag：

```http
If-Match: "tenant-r3"
If-Match: "member-r2"
```

revision 不匹配返回 `409 conflict`；缺少 `If-Match` 返回 `428 precondition_required`。这避免两个页面静默覆盖彼此的修改。

## API

### 枚举授权 Tenant

```http
GET /api/v1/tenants
```

结果包含 stable slug、display name、status、当前 principal 的 role/permissions，以及 quota usage/limits。未知或未授权的 detail locator 都返回相同的 `404 not_found`。

### 创建 Tenant

```http
POST /api/v1/tenants
X-CSRF-Token: <csrfToken>
Content-Type: application/json

{
  "slug": "studio",
  "displayName": "Studio",
  "description": "Product delivery workflows"
}
```

slug 使用 1–40 字符的小写 DNS label。display name 是单行 plain text；description 可为空，最多 500 个 Unicode code points 和 10 行。成功返回 `201`，creator 成为 owner。

### 查看或更新 Tenant

```http
GET /api/v1/tenants/studio
```

具有成员管理权限时，detail 返回完整 member 列表；否则只返回当前 principal 自己的 membership。

```http
PATCH /api/v1/tenants/studio
X-CSRF-Token: <csrfToken>
If-Match: "tenant-r1"
Content-Type: application/json

{
  "displayName": "Studio Team",
  "description": "Product delivery workflows",
  "quotaPolicy": {
    "maxReservedCPU": "2",
    "maxReservedMemory": "2Gi",
    "maxActiveWorkerPods": 4,
    "maxActiveReleases": 4,
    "maxConcurrentRuns": 16,
    "maxConcurrentDeployments": 1
  }
}
```

### 管理成员

```http
POST /api/v1/tenants/studio/members
X-CSRF-Token: <csrfToken>
Content-Type: application/json

{"principalId":"bob","role":"operator"}
```

只接受服务端 catalog 中已存在的 principal。修改 role 和移除成员分别使用：

```http
PATCH  /api/v1/tenants/studio/members/bob
DELETE /api/v1/tenants/studio/members/bob
```

二者都要求 `X-CSRF-Token` 和成员 ETag 对应的 `If-Match`。管理动作及授权失败都会写入 Tenant-scoped Audit，但不会记录 credential 或伪造的 Tenant identity。
