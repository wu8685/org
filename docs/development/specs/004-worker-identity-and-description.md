# Worker identity 与 description 规格修订

> Terminology: this specification follows the canonical [org glossary](../../user/architecture/glossary.md). Product isolation is a Tenant; the underlying resources are the shared platform Temporal Namespace and platform Kubernetes Namespace.

## 状态

**Approved — implementation authorized on 2026-08-01.**

本文件是对以下已批准规格的拟议修订：

- `001-worker-hosting-mvp.md` 中独立 `scope` 的公开模型；
- `002-hello-worker-sample.md` 中 request / metadata 重复 `scope` 的样例契约；
- `003-multi-tenant-shared-infrastructure.md` 中以 `{tenantID, scope}` 构造底层 identity 的规则。

本修订已获确认并授权实施。公开产品、API、未来 UI 与 metadata 中不再存在 `scope`；内部如需 routing key，只能作为由 Tenant + Worker name 派生的不可伪造实现细节，不能以 scope 名义返回。

## 产品决策与目标

对用户公开的稳定执行边界是 **Tenant 内的逻辑 Worker**。独立 `scope` 不再是领域对象或 API 字段：

```text
logical Worker identity = (authenticated tenant, canonical worker name)
```

同一个逻辑 Worker 可以连续发布多个 WorkerVersion。它们共享一个 Temporal Task Queue 与一个 Temporal Worker Deployment；WorkerVersion 仍映射为 Temporal Worker Deployment Version / Build ID。Worker 的 `currentVersion` 指向当前默认版本。Pinned Workflow、Current version、显式历史版本与 Activity 幂等约束保持不变。

每个 WorkerVersion 增加独立 `description`，作为该版本面向人阅读的 release note，说明这个版本做什么或发生了什么变化。description 属于 WorkerVersion/Release，不属于逻辑 Worker；创建每个版本时都必须填写。

## 领域模型

### Logical Worker

```text
Worker {
  tenantID          derived from AuthenticatedContext, never accepted from request
  name              immutable, tenant-local stable name
  currentVersion    nullable version selected as Current
  createdAt
  updatedAt
}
```

唯一性：

```text
UNIQUE (tenant_id, worker_name)
```

`Worker.name` 使用小写 DNS label 子集，建议最大 48 字符。name 创建后不可变；“重命名”会改变 Task Queue、Worker Deployment、Workflow ID 与 Kubernetes identities，因此不属于普通 update。如未来需要重命名，必须另写 migration / drainage spec。

### WorkerVersion

```text
WorkerVersion {
  tenantID
  workerName
  version
  description
  revision          optimistic-concurrency revision for description updates
  imageDigest
  metadata
  runtime
  sourceProvenance
  temporalBuildID
  canonical runtime references
  state / health / current
  createdAt
  updatedAt
}
```

唯一性：

```text
UNIQUE (tenant_id, worker_name, version)
```

WorkerVersion 保存自己的 description。WorkerVersion detail、Run detail 与版本选择界面展示所选版本的 description；不得从逻辑 Worker 继承或回填。Run 创建时记录 selected WorkerVersion，因此历史 Run 始终关联正确版本说明。

### Worker metadata

公开的 WorkerVersion metadata：

```text
WorkerMetadata {
  workflows
  activities
}
```

它不包含 `scope`，也不重复 `workerName`。Worker identity 只来自 authenticated Tenant + tenant-qualified API path/application argument；metadata 不能重新选择 Worker，也不能参与 Tenant 授权。

Workflow / Activity contracts 可以随 WorkerVersion 演进，仍属于 WorkerVersion metadata。description 不进入 `WorkerMetadata` artifact；它由注册 WorkerVersion 的请求单独承载，避免把面向人的 release note 与可执行 contract 混在一起。

## API 与认证边界

### 创建 Worker

```http
POST /workers
```

```json
{
  "name": "payments-worker"
}
```

Tenant 只来自 `AuthenticatedContext`。body、query、path 或自由 header 中的 `tenantId` 继续由 strict schema 拒绝。

行为：

1. require `worker:create`；
2. validate name；
3. 在 `{tenantID, name}` 上原子创建；
4. 重复 name 返回 tenant-local conflict；
5. 写入 tenant-scoped Audit。

### 创建 WorkerVersion

```http
POST /workers/{workerName}/versions
```

body 包含 image digest、version、独立 description、version-specific metadata、runtime 与 source provenance；不包含 `tenantId` 或 `scope`。description 是注册请求字段，不嵌入 metadata。服务端先用 authenticated Tenant 加 path worker name 查出逻辑 Worker，再做 quota admission 和底层部署；成功成为 Current 后原子更新 Worker.currentVersion。

推荐请求形状：

```json
{
  "version": "2026.08.1",
  "description": "Adds payment reconciliation and improves retry diagnostics.",
  "image": "registry.example.com/acme/payments@sha256:...",
  "metadata": {
    "workflows": [],
    "activities": []
  },
  "runtime": {},
  "source": {}
}
```

### 更新 WorkerVersion description（推荐方案）

```http
PATCH /workers/{workerName}/versions/{version}
If-Match: <revision>
```

```json
{
  "description": "Adds payment reconciliation and documents retry diagnostics."
}
```

推荐允许创建后 PATCH description，以便修正错字或补充 release note；description 不是镜像 provenance 或可执行 contract，不必为了文案修订发布新镜像。PATCH 只允许修改 description，并原子增加 WorkerVersion.revision。`version`、image digest、runtime、metadata、source provenance、Temporal Build ID、canonical names、Current 状态均不可由此接口改变；unknown field、空 PATCH 与 revision 不匹配返回 conflict/validation error。

触发语义为 `POST /workers/{workerName}/workflows/{workflowName}/runs`。可选 `workerVersion` 指定历史 ready version；缺省时使用 Worker.currentVersion。Run、Signal、Query、cancel 与列表接口同样以 tenant-qualified Worker identity 工作，不接受 `scope`。idempotency lookup 改为：

```text
UNIQUE (tenant_id, worker_name, workflow_name, idempotency_key)
```

## Description 内容策略

MVP 使用 **plain UTF-8 text**，不解释 Markdown/HTML：

- trim 首尾空白；内部换行保留；
- 创建时必填，长度为 1–2,000 Unicode code points；
- 拒绝 NUL 与不可见 control characters，允许普通换行与 tab；
- UI 按 escaped plain text + `white-space: pre-wrap` 渲染，禁止 `innerHTML`；
- API 返回指定 WorkerVersion 的完整文本；版本列表/选择界面显示单行截断摘要，WorkerVersion detail 与 Run detail 显示全文；
- WorkerVersion create 必填；之后只能通过指定 version 的 PATCH 修改，逻辑 Worker create/update 不接受 description。

选择 plain text 的原因是 description 当前承担版本说明，不需要链接、表格或富文本；它能减少 sanitizer、XSS 与不同客户端渲染不一致。若未来需要 Markdown，应限定 CommonMark 子集、禁用 raw HTML，并把 sanitizer 与 link policy 纳入验收测试。

## Server-side canonical naming

所有构造器从 immutable `tenantID` 与 canonical Worker name 派生：

```text
tenantWorkerKey = <tenant-slug>-<worker-slug>-<hash10>
hash10 = first 10 lower-case base32 chars of SHA-256(tenantID + NUL + canonicalWorkerName)
```

映射：

| 资源 | 规范形式 |
|---|---|
| Temporal Task Queue | `org-<tenantWorkerKey>` |
| Temporal Worker Deployment | `org-<tenantWorkerKey>` |
| Temporal Workflow ID | `org-<tenantWorkerKey>-run-<opaqueRunID>` |
| Kubernetes Deployment | `org-<tenantWorkerKey>-<versionHash>` |
| Kubernetes ServiceAccount | `org-<tenantWorkerKey>` |
| Kubernetes NetworkPolicy | `org-<boundedTenantWorkerKey>-np-<versionHash>` |

Kubernetes 63 字符预算、deterministic truncation 与“末尾 identity hash 不截断”规则沿用 003。底层 adapter 不得保留 scope fallback 或自行拼接旧名字。

Kubernetes labels 调整为：

```text
app.kubernetes.io/managed-by=org
org.wu8685.dev/tenant=<tenant-slug>
org.wu8685.dev/tenant-hash=<tenant-id-hash>
org.wu8685.dev/worker=<worker-slug>
org.wu8685.dev/version=<version-hash>
```

移除 `org.wu8685.dev/scope`。quota、ServiceAccount 无 token、resources、NetworkPolicy 边界，以及共享 platform Temporal Namespace / platform Kubernetes Namespace 的限制保持不变。

## Audit

WorkerVersion create / description update 至少记录：

- tenant ID / slug、principal、authentication method、request ID；
- action、authorization result、outcome；
- Worker tenant-scoped ID/name、version、WorkerVersion revision；
- description 的旧/新 Unicode 长度与 SHA-256 digest，不在 Audit payload 中复制完整 description；
- timestamp 与稳定 error class。

完整 description 只保存在 WorkerVersion 业务记录中。Audit 在 create/update 时记录 description digest 与长度；这样既能证明某次更新确实改变了内容，又减少 description 中误写内部信息后在 immutable Audit 中形成第二份长期副本。审计读取继续要求 tenant-scoped `audit:read`。

WorkerVersion / Run Audit 用 `workerName` 取代旧 `scope` 字段，并记录 tenant-aware canonical references。不得记录 Temporal/Kubernetes credentials 或 Secret value。

## 生命周期与兼容性

- WorkerVersion description update 不创建新版本，不触发 Worker rollout、Task Queue 变化、Current 切换或正在运行的 Pinned Workflow migration。
- Tenant 处于 active / suspended / deleting 生命周期时，WorkerVersion description update 遵循 003 的规则：suspended / deleting 禁止普通非只读 mutation。
- Worker delete 必须先盘点所有 WorkerVersions 与 open Pinned Runs；本修订不授权实现物理删除。
- 旧 `{tenantID, scope, workerName}` 记录不能靠删除字段原地猜测归属。迁移必须验证每个旧 scope 是否恰好对应一个 Worker；若一个 scope 存在多个 Worker 或 scope 与 Worker name 不同，需要显式 migration mapping。
- 迁移期间不得让新旧 Task Queue 同时被同一逻辑 Worker 无控制地 poll；旧 Pinned Runs 需要 drainage 或受控 compatibility route。

## 验收场景

1. Create Worker request 只包含 name；description 只出现在 WorkerVersion create/PATCH。伪造 `tenantId` 或 `scope` 被 strict schema 拒绝。
2. 两个 Tenant 可以创建同名 Worker；同一 Tenant 不能创建两个同名 Worker。
3. 同一 Tenant 的两个 WorkerVersion 共享 Task Queue / Worker Deployment，但 Kubernetes Deployment name 与 version hash 不同；成为 Current 后 Worker.currentVersion 正确更新。
4. 不同 Tenant 的同名 Worker 得到不同 Task Queue、Worker Deployment、Workflow ID、ServiceAccount 与 workload name。
5. metadata 没有 scope；adapter、store、idempotency key 与 audit 查询也不存在不带 Tenant 的 scope fallback。
6. 每个 WorkerVersion create 都必须填写自己的 description；版本列表/选择、WorkerVersion detail 与关联 Run detail 展示所选版本的说明，逻辑 Worker 不展示或存储一份共享 description。
7. 指定 version 的 description PATCH 使用 WorkerVersion revision 防丢失更新；更新不会改变 image/runtime/metadata/source/Temporal Build ID、rollout Worker、切换 Current 或改变 canonical runtime name。
8. plain text 被 escaped render；超长文本、NUL、control characters、HTML/Markdown 注入不被当作 markup 执行。
9. Audit 记录 description change 的 digest/length 与 revision，不包含完整 description 或 credential。
10. 双 Tenant kind/Temporal E2E 改用相同 Worker name（不再构造 scope），继续证明 Current、显式历史版本、projection 与无数据/命名串扰。

## 已确认的实施规则

1. description 采用 1–2,000 Unicode code points 的 plain text；UI escaped/pre-wrap，不支持 Markdown。
2. 每个 WorkerVersion create 必须带 description；description 是注册请求的独立字段，不进入 WorkerMetadata artifact。
3. 创建后允许对指定 version PATCH description，使用 WorkerVersion revision / `If-Match`；只改文案，不改变任何 immutable artifact/runtime/Temporal identity。相较“完全不可变 release note”，这是推荐方案，因为可修正文案且不伪造新版本。
4. Audit 只记录 old/new description digest、长度、version 与 WorkerVersion revision，不复制全文。
5. permissions 使用 `worker:create`、`worker:deploy` 与独立 `worker:version:update`；逻辑 Worker 不再需要 `worker:update`。
6. WorkerVersion metadata 同时删除 `scope` 与重复 `workerName`；Worker identity 只来自 authenticated Tenant + API path/application argument。

按 TDD 修改领域/store/application contracts，再迁移 canonical naming 与 Kubernetes/Temporal adapters，随后更新 sample metadata，最后把双 Tenant E2E 从 scope 模型切到 Worker identity 模型。
