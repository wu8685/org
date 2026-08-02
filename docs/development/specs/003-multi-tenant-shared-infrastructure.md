# org 多租户共享基础设施设计

> Terminology: this specification follows the canonical [org glossary](../../user/architecture/glossary.md). Product/API/UI isolation is called Tenant; a compatibility UI label must read Namespace (Tenant). The platform currently has exactly one shared platform Temporal Namespace and one shared platform Kubernetes Namespace.

> Approved amendment: `004-worker-identity-and-description.md` replaces `{tenantID, workerName}` runtime identity with `{tenantID, workerName}`.

## 状态

**Approved — implementation authorized on 2026-08-01.**

本文件取代 `001-worker-hosting-mvp.md` 中的 “One tenant” 假设，以及任何把 Tenant 一对一映射为 platform Temporal Namespace / platform Kubernetes Namespace 的设计。实现仍须保持共享 platform Temporal Namespace 与共享 platform Kubernetes Namespace 的边界。

## 背景与已确定方向

`org` 将支持多个 Tenant，但暂不采用下列一对一隔离方式：

- `Tenant -> platform Temporal Namespace`；
- `Tenant -> platform Kubernetes Namespace`。

平台使用：

- 一个可配置的 **platform Temporal Namespace**；
- 一个可配置的 **platform Kubernetes Namespace**。

这样做是为了保留底层资源共享、降低小规模 Tenant 的固定成本，并允许平台统一调度 Worker。相应代价是：Tenant 隔离主要由 `org` 控制面、命名、授权、配额、凭证管理与审计共同实现，不得把共享 platform Temporal Namespace 或共享 platform Kubernetes Namespace 宣称为底层平台提供的硬多租户隔离。

本设计沿用一条跨系统不变量：**Tenant identity 必须贯穿认证、授权、数据、运行时命名、配额与审计全链路；受保护操作必须经过统一的 `org` Gateway。**

## 目标

1. 将 Tenant 建模为一等领域对象，而不是 Worker 上的可选字符串。
2. 防止 API、持久化、Temporal 标识与 Kubernetes 资源发生跨 Tenant 混淆或碰撞。
3. 确保最终用户只能通过 `org` 的认证与授权入口操作本 Tenant 的 Worker、Release 和 Run。
4. 在共享 platform Kubernetes Namespace 中提供 Tenant 级资源预算、并发限制与可审计的 admission decision。
5. 如实记录共享 platform Temporal Namespace / platform Kubernetes Namespace 的安全边界和剩余风险。
6. 保持现有 Worker Versioning、Pinned Workflow、Activity 幂等与语义化 projection 设计不变，但全部增加 Tenant 维度。

## 非目标

- 将共享 platform Temporal Namespace 或共享 platform Kubernetes Namespace 描述成对恶意 Tenant 的硬安全边界。
- 在本阶段为每个 Tenant 创建独立 Kubernetes cluster、platform Kubernetes Namespace 或 platform Temporal Namespace。
- 允许终端用户直接访问 Temporal frontend、Temporal Web 或 Kubernetes API。
- 让 Kubernetes `ResourceQuota` 原生承担按 label 的 Tenant 配额；它不支持这一语义。
- 在本草案阶段实现计费、跨集群调度、Tenant 自助 RBAC 或复杂优先级抢占。
- 修改 UI；UI 的多租户信息架构另行设计。

## 威胁模型与隔离声明

### 需要防止

- 用户伪造 `tenantId` 读取、启动、Signal、Query、取消或部署其他 Tenant 的资源。
- 数据库查询漏掉 Tenant 条件导致 IDOR 或跨 Tenant 列表泄漏。
- 两个 Tenant 使用相同 Worker name、version 或 Run ID 时发生 Task Queue、Worker Deployment、Workflow ID 或 Kubernetes 资源碰撞。
- 一个 Tenant 超量部署或启动 Run，耗尽共享 CPU、内存、Pod 数或 Worker poller。
- Worker Pod 获得不需要的 Kubernetes API 权限。
- Temporal 或 Kubernetes 凭证出现在浏览器、CLI 响应、日志、审计 payload 或用户可下载的配置中。

### 本设计不能单独防止

- 恶意或已被攻陷的 Worker 利用共享 node/kernel、CNI、容器运行时或平台级 Temporal 凭证攻击其他 Tenant。
- CNI 不支持或未启用 NetworkPolicy 时的 Pod 网络互访。
- platform Temporal Namespace 级凭证被 Worker 窃取后的跨 Task Queue 访问。platform Temporal Namespace 共享并不天然提供 Task Queue 级 authorization boundary。
- Kubernetes control-plane、Temporal server 或 `org` 自身被攻陷后的跨 Tenant 访问。

因此，本阶段适合“平台治理下的受信或经过审核的 Worker”场景。若必须承载互不信任的恶意代码，需升级到更强隔离层，例如独立 platform Kubernetes Namespace/cluster、sandbox runtime、按 Tenant 分配的 platform Temporal Namespace，或能强制校验 Worker identity 与 Task Queue 的受控代理；仅靠本规格不足以满足该威胁模型。

## 领域模型

### Tenant

```text
Tenant {
  id                 opaque, immutable platform ID
  slug               unique, stable DNS-safe slug
  displayName        user-facing name
  status             active | suspended | deleting
  quotaPolicy        TenantQuotaPolicy
  createdAt
  updatedAt
}
```

约束：

- `Tenant.id` 是内部主键和审计依据，不复用、不从 display name 推导。
- `Tenant.slug` 只用于可读的运行时命名；创建后默认不可变。
- slug 必须全局唯一，符合小写 DNS label 子集，并保留足够长度给 workerName、release 与 hash。
- 删除 Tenant 采用显式生命周期，不能仅删除数据库行而遗留 Worker 或 Pinned Run。

### Tenant-scoped 对象

下列对象必须包含不可为空的 `tenantID`：

- Worker；
- Release / Deployment；
- Run / Invocation；
- Workflow semantic projection snapshot；
- idempotency-key record；
- quota reservation / concurrency lease；
- Audit record；
- source provenance 与 image identity 记录。

所有唯一性约束必须包含 `tenantID`，例如：

```text
UNIQUE (tenant_id, worker_name, version)
UNIQUE (tenant_id, worker_name, workflow_name, idempotency_key)
INDEX  (tenant_id, created_at)
```

全局 opaque ID 可以保持全局唯一，但通过 ID 查询后仍必须校验记录的 `tenantID == authenticatedTenantID`。知道另一个 Tenant 的 opaque ID 不构成授权。

## 认证主体与 Tenant 派生

### 不变量

任何普通读写 API 都必须先由认证层产生：

```text
AuthenticatedContext {
  principalID
  tenantID
  roles / permissions
  authenticationMethod
  requestID
}
```

Application Service 接收 `AuthenticatedContext`，而不是接收来自 JSON、query、header 或 path 的自由 `tenantId`。

禁止：

- 客户端在 deployment、Run、Signal、Query、cancel 请求中提交 `tenantId` 并由业务代码信任；
- 用可伪造的 `X-Tenant-ID` 直接建立 Tenant context；
- 先按资源 ID 读出完整记录，再在响应层过滤 Tenant；
- store 方法提供不带 Tenant 的普通业务查询，例如 `Invocation(id)`。

要求：

- Tenant 来自经验证的 token claim、server-side session，或认证后由服务端解析的 active-tenant selection。
- 多 Tenant membership 的 active Tenant 切换必须生成新的、经服务端校验的认证上下文；不能只修改请求参数。
- 所有 store 方法以 `tenantID` 为必填首要条件，例如 `Invocation(tenantID, id)`。
- “not found” 与 “属于其他 Tenant” 对普通用户返回相同外部语义，避免资源枚举。
- 平台管理员的跨 Tenant 操作走独立 admin authorization path，并在审计中同时记录 operator Tenant / target Tenant；不得复用普通用户的伪造 `tenantId` 参数。

## Server-side canonical naming

所有底层名字只由 `org` 服务端构造。客户端提供的 Worker name 或 version 只能作为经过验证的领域输入；Tenant 来自认证上下文，任何字段都不能直接拼接进命令或 YAML。

### Canonical identity

```text
tenantWorkerKey = <tenant-slug>-<worker-slug>-<hash10>
hash10 = first 10 lower-case base32 chars of SHA-256(tenantID + NUL + canonicalWorkerName)
```

设计意图：

- slug 使运维人员可读；
- hash 使 Tenant ID / workerName 同名或截断后仍稳定防碰撞；
- hash 输入使用不可变 `tenantID`，不能只依赖可重命名 display name；
- 所有构造器统一做长度预算和 deterministic truncation。

### 名称映射

| 资源 | 规范形式 |
|---|---|
| Temporal Task Queue | `org-<tenantWorkerKey>` |
| Temporal Worker Deployment | `org-<tenantWorkerKey>` |
| Temporal Workflow ID | `org-<tenantWorkerKey>-run-<opaqueRunID>` |
| Kubernetes Deployment | `org-<tenantWorkerKey>-<versionHash>` |
| Kubernetes ServiceAccount | `org-<tenantWorkerKey>` |
| Kubernetes Secret | `org-<tenantWorkerKey>-<purposeHash>` |

附加约束：

- 一个 `{tenantID, workerName}` 对应一个 Task Queue 与一个 Temporal Worker Deployment；不同 Tenant 永不共享它们。
- Worker version 仍映射为该 Worker Deployment 下的 Temporal Deployment Version。
- 显式历史版本仍使用 Pinned Versioning Override，但 lookup 必须限定 Tenant。
- Kubernetes 名称必须符合 63 字符 DNS label 上限；超长部分先按统一规则截断，末尾 hash 永不截断。
- Workflow ID 中的 opaque Run ID 不接受用户指定的完整底层 ID。用户业务 correlation ID 单独保存。
- 名称构造器必须是唯一实现，禁止各 adapter 自行拼接。

## 共享 platform Kubernetes Namespace

### 资源标记

`org` 创建的每个 Namespaced 资源，包括 Deployment、ReplicaSet/Pod template、ServiceAccount、Secret、ConfigMap、Service 与 NetworkPolicy，必须带至少以下 label：

```text
app.kubernetes.io/managed-by=org
org.wu8685.dev/tenant=<tenant-slug>
org.wu8685.dev/tenant-hash=<tenant-id-hash>
org.wu8685.dev/worker=<worker-slug>
org.wu8685.dev/version=<version-hash>
```

label 用于可观测性、清理、policy selection 与对账，不是授权依据。`org` 的授权仍使用持久化 `tenantID`。

### ServiceAccount

- 默认每个 `{tenantID, workerName}` 一个稳定 ServiceAccount，所有 Release 复用它。
- `automountServiceAccountToken: false`。
- 默认不创建 Role、RoleBinding 或 ClusterRoleBinding。
- Worker 默认没有 Kubernetes API 凭证和权限。
- 只有经单独批准的 workload identity 集成可以改变此默认值，并必须记录 Tenant、用途、权限和审批审计。

### Pod 资源约束

每个 Worker Pod 必须设置：

- CPU request 与 limit；
- memory request 与 limit；
- 可选 ephemeral-storage request 与 limit；
- non-root、read-only root filesystem、drop capabilities 等现有安全基线。

缺少资源 requests/limits 的 Release 在 admission 时被拒绝，不能依赖 Kubernetes 默认值静默补齐。

### Tenant quota 与并发限制

`org` 维护 `TenantQuotaPolicy`：

```text
TenantQuotaPolicy {
  maxReservedCPU
  maxReservedMemory
  maxActiveWorkerPods
  maxActiveReleases
  maxConcurrentRuns
  maxConcurrentDeployments
}
```

行为：

1. 部署或 Run 启动前，控制面在持久化层原子申请 quota reservation / concurrency lease。
2. 超限请求在创建 Kubernetes/Temporal 资源前失败，返回稳定、可审计的 quota exceeded 语义。
3. 成功、失败、取消、超时与 reconciliation 都必须释放或修正 lease。
4. 后台 reconciler 定期将控制面账本与 Kubernetes/Temporal 实际状态对账，处理进程崩溃造成的悬挂 reservation。
5. 共享 platform Kubernetes Namespace 可额外配置平台级 `ResourceQuota` 与 `LimitRange`，用于保护整个 platform Kubernetes Namespace，但它们不能替代 Tenant quota。

**明确限制：Kubernetes `ResourceQuota` 不能原生按 tenant label 分账或限额。** 因此 Tenant quota 是 `org` admission + ledger + reconciliation 能力。若绕过 `org` 直接向共享 platform Kubernetes Namespace 创建资源，该资源不受 Tenant 账本正确治理；生产环境必须限制谁能直接写该 platform Kubernetes Namespace。

### NetworkPolicy

NetworkPolicy 是可选的 defense-in-depth：

- 可按 tenant/workerName label 限制 Worker ingress；默认 Worker 无需接受业务 ingress。
- 可限制 egress 到 DNS、Temporal endpoint 与 metadata 声明/平台批准的外部服务。
- 只有 CNI 明确支持并启用 NetworkPolicy 时才生效。
- kind/local 环境未必具有与 production 相同的 policy enforcement，测试必须区分“manifest 已生成”与“网络隔离已实际执行”。

共享 platform Kubernetes Namespace + label-selected NetworkPolicy 不是硬 Tenant 隔离，文档、UI 和安全评估不得作此宣称。

## 共享 platform Temporal Namespace

### 路由与对象隔离

- 所有 Tenant 共用一个 platform Temporal Namespace。
- Task Queue、Worker Deployment 与 Workflow ID 使用 server-side canonical tenant naming。
- `org` 在启动、Describe、Query、Signal、cancel、result、历史版本选择和诊断 deep link 前完成 Tenant authorization。
- 普通用户不直接调用 Temporal API；所有 user-plane 操作通过 `org` Gateway。
- Temporal Web deep link 不能直接暴露给普通用户，除非前置代理能对 Tenant/Run 做同等 authorization。平台运维诊断入口与普通用户入口分离。

### 凭证

- 浏览器、普通 CLI、API response、Worker metadata 和 source provenance 中不返回 Temporal client credentials。
- `org` control plane 的 Temporal credentials 仅在服务端 secret store / workload identity 中加载。
- Worker 所需连接凭证由 `org` 在部署时以 Secret 或 workload identity 注入，不进入用户仓库、镜像 label 或审计 payload。
- credential material 不能进入日志；审计只记录 credential reference/version，不记录 secret value。

### 共享 platform Temporal Namespace 的限制与风险

platform Temporal Namespace 是重要的 authorization 和 data boundary。共享 platform Temporal Namespace 意味着：

- platform Temporal Namespace 级 credential 通常可见或可操作该 platform Temporal Namespace 内不止一个 Tenant 的 execution；
- Task Queue 命名防碰撞，但命名本身不是 cryptographic authorization；
- compromised Worker 若持有可直接连接 Temporal 的共享 platform Temporal Namespace credential，可能尝试 poll、start、signal 或 query 其他 Tenant 的已知对象；
- `org` Gateway 能保护终端用户发起的 control-plane 操作，但不能自动约束绕过 Gateway 的恶意 Worker data-plane 行为。

在没有 Task Queue 级 credential enforcement 或受控 Worker-to-Temporal proxy 之前，本方案的 Temporal 隔离属于**平台逻辑隔离**，不是针对恶意 Worker 的硬隔离。该风险必须进入发布门槛、威胁模型与运维手册。

## Application authorization

建议权限对象：

```text
worker:read | worker:deploy | worker:retire
run:start | run:read | run:signal | run:query | run:cancel
audit:read
tenant:quota:read | tenant:admin
```

每个 use case 的顺序固定为：

```text
authenticate principal
  -> derive authenticated tenant
  -> authorize verb on tenant-scoped resource
  -> reserve tenant quota if needed
  -> load/mutate data with tenant-qualified key
  -> call Kubernetes/Temporal adapter with canonical names
  -> append tenant-scoped audit record
```

不能把 adapter 的 NotFound、Temporal Workflow ID 或 Kubernetes resource name 当成授权结果。

## Audit

每条 Audit record 必须包含：

- `tenantID` 与当时的 tenant slug；
- principal ID、认证方式和授权结果；
- action / permission；
- target type 与 tenant-scoped target ID；
- request ID / correlation ID；
- success / denial / failure outcome；
- quota reservation 或 rejection 信息；
- image digest、source provenance、workerName、Release version；
- canonical Task Queue、Worker Deployment、Workflow ID 与 Kubernetes workload reference（内部审计字段）；
- timestamp 与错误分类，且不包含 secret value。

跨 Tenant admin 操作同时记录 operator context 与 target Tenant。审计查询本身也必须 tenant-scoped；普通用户不能通过审计搜索推断其他 Tenant 的对象名称或用量。

## Tenant 生命周期

### Active

允许在权限与 quota 内执行正常读写。

### Suspended

至少禁止新 Release、新 Run、Signal 与非只读 mutation。是否允许既有 Pinned Run 继续完成、仅允许 cancel、或主动终止，需要用户确认；不能静默删除其旧 Worker version。

### Deleting

进入受控清理流程：

1. 阻止新 mutation；
2. 盘点 open Pinned Run；
3. 按已批准策略等待、取消或迁移；
4. 删除 Tenant 标记的 Kubernetes workloads/secrets/service accounts；
5. 清理 Temporal Worker Deployment versions（满足 drainage 条件后）；
6. 保留或归档审计；
7. 最后删除 Tenant 业务数据。

任何清理都必须同时匹配内部 `tenantID` 与 canonical labels/names，不能只用用户输入的 slug 批量删除。

## 错误语义

- 未认证：`unauthenticated`。
- 无 Tenant membership / permission：`permission_denied`；不透露目标是否存在。
- Tenant suspended：`tenant_suspended`。
- quota 超限：`tenant_quota_exceeded`，返回可公开的 quota category，不泄露其他 Tenant 用量。
- 同 Tenant 内重复 version/idempotency key：沿用现有 conflict / idempotent replay 语义。
- adapter 中发现 canonical name 与持久化 tenant identity 不一致：视为 integrity violation，停止 mutation 并告警，不能自动“修正到另一个 Tenant”。

## 可观测性

所有 metric、log 与 trace 至少携带不可逆 tenant hash，而不是默认暴露原始 Tenant ID。高基数 workerName/run ID 是否进入 metric label 需受控；完整 ID 放 trace/log field。

Tenant 维度最低观测项：

- reserved CPU / memory；
- active Worker Pods / Releases；
- concurrent Runs 与 quota rejection；
- deployment readiness / Worker poller health；
- Run start/success/failure/cancel；
- authorization denial；
- reconciliation drift。

## 迁移原则

现有单 Tenant 数据不能在实现时隐式视为任意请求 Tenant。正式迁移需要：

1. 创建一个明确的 legacy/default Tenant；
2. 为现有 Worker、Deployment、Invocation 与 Audit 回填其 `tenantID`；
3. 重新计算 tenant-aware canonical names；
4. 对仍在运行的 Pinned Workflow 制定兼容/排空方案，不能直接改 Workflow ID 或 Task Queue；
5. 在所有 store/API 已强制 Tenant 条件后，才开放第二个 Tenant。

具体 migration 和 backward compatibility 不在本草案的实现授权内。

## 验收场景

1. Tenant A 与 Tenant B 使用相同 Worker name/version，得到不同且稳定的 Task Queue、Worker Deployment、Workflow ID 与 Kubernetes names。
2. A 的 principal 即使知道 B 的 Worker/Run opaque ID，也无法 read、Query、Signal、cancel 或获得“对象存在”提示。
3. deployment/run 请求携带伪造 `tenantId` 时被 schema 拒绝或完全忽略；实际 Tenant 只来自 authenticated context。
4. 列表、search、idempotency lookup、audit 与 result lookup 均按 Tenant 限定。
5. 所有 Kubernetes 资源带 tenant/worker/version labels，Pod 有 requests/limits，ServiceAccount 不挂载 token 且无 RoleBinding。
6. 两个 Tenant 的同名 Worker 可在共享 platform Kubernetes Namespace 同时运行，不发生资源名碰撞。
7. 两个 Tenant 的同名 Worker 可在共享 platform Temporal Namespace 同时执行，不共享 Task Queue 或 Worker Deployment。
8. Tenant quota 并发竞争时只有额度内请求成功；控制面崩溃后的 lease 可由 reconciliation 修正。
9. platform Kubernetes Namespace 级 `ResourceQuota` 只作为平台总量保护，测试和文档不把它当成按 label Tenant quota。
10. NetworkPolicy 未被 CNI enforce 时系统明确报告“未提供网络隔离”，而不是健康通过。
11. API、日志、审计、projection 和错误响应均不包含 Temporal/Kubernetes secret value。
12. 每次授权允许、拒绝、quota 拒绝和跨 Tenant admin 操作都写入含 `tenantID` 的 Audit record。
13. Tenant suspended/deleting 时，现有 Pinned Workflow 按已确认的生命周期策略处理，不被静默遗弃。

## 已批准的 MVP 默认策略

### 1. 多 Tenant membership 如何选择 active Tenant

active Tenant 由 server-side session 或短期 token 的已验证 claim 固化；切换 Tenant 需要服务端重新签发上下文。普通 DTO 不接受自由 `tenantId` header/body/query/path 字段，strict JSON decoding 会拒绝未知的 `tenantId`。

### 2. Tenant slug 是否允许重命名

Tenant slug 创建后不可变，display name 可变。任何未来的 slug migration 必须另写 migration spec，不能在普通更新中隐式重命名底层资源。

### 3. 共享 Temporal credential 风险是否可接受

MVP 只接收受信或经过平台审核的 Worker image，并明确接受共享 platform Temporal Namespace 下的逻辑隔离风险。当前版本不承诺抵御持有共享 platform Temporal Namespace credential 的恶意 Worker；若威胁模型扩大，必须先增加 Worker-to-Temporal authorization proxy、更细 credential boundary 或独立 platform Temporal Namespace。

### 4. Tenant quota 的默认值与超限策略

新 Tenant 的安全默认值为：reserved CPU `2`、reserved memory `2Gi`、active Worker Pods `4`、active Releases `4`、concurrent Runs `16`、concurrent Deployments `1`。Tenant 创建时可以显式设置另一组有限正值。MVP 采用 atomic admission 后 hard reject，不提供隐式排队、burst、抢占。

### 5. Tenant suspended 时如何处理既有 Pinned Run

既有 Pinned Run 与旧 Worker version 默认保留并允许自然完成；禁止新 Release、新 Run 与 Signal。只读状态仍可读取；cancel 需要 `run:cancel` 与 `tenant:admin`。不得因 suspension 静默删除旧 Worker version。

### 6. NetworkPolicy 的发布门槛

local kind 可以只验证 NetworkPolicy manifest，但状态必须是 `manifest_only_not_enforced`，不能声称网络隔离成立。production 默认要求 CNI 明确支持并 enforce NetworkPolicy；不满足时发布检查应拒绝，除非另有显式风险接受流程。本 MVP 的最小 policy 先关闭 Worker ingress；allowlisted egress 需要在外部目的地契约确定后单独扩展。

### 7. ServiceAccount 粒度

每 `{tenantID, workerName}` 一个 server-generated ServiceAccount，Release 复用；`automountServiceAccountToken: false`，不创建任何 RBAC binding。MVP 不提供 Worker 访问 Kubernetes API 的例外入口，未来例外须另写权限与审批规格。

### 8. legacy 单 Tenant 数据归属

本实现不执行 legacy 数据 migration，也不把缺少 `tenantID` 的旧记录隐式归入当前请求 Tenant。legacy 归属与现有 Pinned Workflow 排空仍须单独 migration spec；在此之前旧记录对多租户业务查询不可见。

### 9. Audit 保留与 Tenant 删除

MVP 不实现 Tenant 物理删除，也不自动删除 Audit。Audit 与业务数据分开考虑生命周期，保留 immutable tenant ID reference 且不记录 secret。具体保留期限、slug 匿名化与合规删除须在启用 Tenant deletion 前由独立 retention spec 确定。
