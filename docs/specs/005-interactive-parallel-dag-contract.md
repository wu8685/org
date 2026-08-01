# 复杂并行 DAG 与人工交互契约

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product isolation is a Tenant; the underlying resources are the shared platform Temporal Namespace and platform Kubernetes Namespace.

## 状态

**Draft — awaiting explicit user approval. No implementation authorization.**

本文件只定义 WorkerVersion metadata、semantic projection 与未来 application/API contract。它不授权修改 domain、service、Temporal/Kubernetes adapter、sample、E2E、UI 或 HTTP handler。用户明确确认后方可按 TDD 实现。

本规格扩展 `001-worker-hosting-mvp.md` 与 `004-worker-identity-and-description.md` 的 Workflow contract；Tenant → Worker → WorkerVersion → Workflow Run、Current/显式历史版本、Pinned Workflow、Activity 幂等和多租户授权边界保持不变。

## 核心产品边界

`org` **不执行业务 DAG，也不是 Workflow engine**。

职责分工固定为：

```text
WorkerVersion metadata
  -> org 验证并保存静态语义 contract

用户 Worker 的 Temporal Workflow
  -> 实际安排并发、Activity、Timer、Signal wait、恢复、取消与补偿

Worker semantic projection query
  -> 返回当前业务节点状态与允许动作

org Gateway
  -> tenant authorization、schema validation、action idempotency、Signal 转发、审计
```

因此：

- fork/join 只在 metadata 中描述可验证的静态语义关系；实际并发由 Workflow 使用 Temporal SDK 的 deterministic concurrency primitives 实现；
- manual interaction node 只描述用户可理解的 action；实际等待与恢复由 Workflow Signal + deterministic state 实现；
- `org` 不根据 metadata 自行 schedule Activity，不维护一套与 Temporal 并行的业务状态机；
- `org` 禁止读取 Temporal Event History 后猜测节点、依赖或业务状态；Event History 仅供受限的高级诊断，不是产品 projection 来源；
- Workflow code 仍不得直接执行外部 I/O；所有外部读写必须经 Activity。

## WorkerVersion metadata contract

每个 WorkerVersion 的 metadata 继续是 version-specific artifact contract，不包含 `tenantId`、`scope`、重复 `workerName` 或版本 description。

推荐 envelope：

```json
{
  "contractVersion": "org.worker/v1",
  "workflows": [
    {
      "name": "OrderApprovalWorkflow",
      "versioningBehavior": "pinned",
      "projectionQuery": "org_projection",
      "inputSchema": {},
      "nodes": []
    }
  ],
  "activities": []
}
```

`contractVersion` 由 `org` 明确支持；unknown major version 必须在部署前拒绝，不能按“尽量解析”降级。

### DAG node 基础字段

```text
DAGNode {
  id                   stable within one Workflow contract
  label                human-readable
  type                 activity | external-service | manual-interaction | semantic
  dependsOn[]          upstream node IDs
  joinPolicy           all  // MVP recommendation
  terminalBehavior     required | optional
}
```

约束：

- `id` 在 Workflow contract 内唯一，使用稳定、不可本地化的安全标识；label 可以面向用户展示；
- 所有 dependency 必须存在；禁止 self dependency 与 cycle；
- `dependsOn: []` 表示 root node；多个 root node 可以并行；
- 多个节点依赖同一上游表示 fork；一个节点依赖多个上游表示 join；
- 推荐 MVP 只支持 `joinPolicy: all`：所有未被 `skipped` 的依赖达到成功终态后，下游才可开始；
- metadata 中的依赖表达业务语义，不要求与 Workflow 内每个低层 Activity 一一对应；Worker 可以在一个语义节点内执行多个实现步骤，但 projection 必须稳定映射回声明节点；
- `org` 只验证图结构，不依据依赖推进状态。

示例 fork/join：

```json
{
  "nodes": [
    {"id":"validate","label":"Validate order","type":"activity","dependsOn":[]},
    {"id":"check-risk","label":"Check risk","type":"external-service","dependsOn":["validate"]},
    {"id":"reserve-stock","label":"Reserve stock","type":"external-service","dependsOn":["validate"]},
    {"id":"manager-approval","label":"Manager approval","type":"manual-interaction","dependsOn":["check-risk"]},
    {"id":"commit","label":"Commit order","type":"activity","dependsOn":["reserve-stock","manager-approval"],"joinPolicy":"all"}
  ]
}
```

实际 `check-risk` 与 `reserve-stock` 是否同时启动、失败后如何等待另一分支、Signal 到达后何时继续，均由用户 Workflow 代码决定。

## Activity 与 external-service node

`activity` 和 `external-service` 节点必须关联一个或多个已声明 Activity contract。`external-service` 只是更明确的产品语义分类，不授权 Workflow code 直接发网络请求。

```text
ActivityNodeContract {
  activityTypes[]
  sideEffect          none | read | write
  retryPolicy {
    initialInterval
    backoffCoefficient
    maximumInterval
    maximumAttempts
    startToCloseTimeout
  }
  idempotencyKey? {
    field
    derivation
    propagation
  }
  reconciliationPolicy?
  compensationPolicy?
}
```

要求：

- 每个 Activity/external-service node 必须声明 retry policy 与 timeout；不能依赖隐藏的 SDK default；
- `sideEffect: write` 必须满足以下之一：
  1. 声明稳定 idempotency key，并明确如何从 Run ID / Activity ID / business key 推导及如何传给 downstream；
  2. 声明可执行的 reconciliation policy，并在无法安全重试时设置 `maximumAttempts: 1`；
  3. 声明 reconciliation + compensation policy，说明如何识别已成功但 completion 未记录的外部效果；
- `org` 只验证 contract 完整性并审计 metadata；它不能承诺外部效果 exactly once；
- Worker 必须正确处理“外部写成功、Worker crash、Temporal 尚未记录 Activity completion”的 ambiguous outcome；已有 crash-safety 测试原则继续适用；
- read Activity 也必须声明 retry/timeout，以便用户理解阻塞与失败行为。

## Manual interaction node

manual interaction node 由 Workflow Signal 实现。终端用户只看到业务 action，不看到 Signal、Task Queue 或 Temporal terminology。

```text
ManualInteractionNode {
  id
  label
  type                 manual-interaction
  dependsOn[]
  actions[] {
    name               public business action name
    label              human-readable action label
    signalName         internal Worker Signal name
    inputSchema        JSON Schema
    requiredPermission org permission
    operationIDRequired true
  }
  waiting {
    status             waiting-for-user
    defaultBlockReason human-readable fallback
    timeoutPolicy {
      duration?
      outcome          fail | cancel | skip | continue-with-default
    }
  }
}
```

约束：

- action `name` 在该 Workflow contract 内唯一且稳定；它是未来 API path 与 Audit action identity；
- `signalName` 只用于 org-to-Worker adapter，不出现在普通 Run/API/UI view；
- `inputSchema` 使用 `org` 支持的 JSON Schema 子集；org 在发送 Signal 前验证 payload，Workflow 仍必须做业务状态与业务约束校验；
- `requiredPermission` 必须来自平台 permission catalog，不能通过 metadata 创建任意高权限字符串；
- manual node 进入等待时，projection 必须返回 `waiting-for-user`、当前 block reason 与当下允许的 action；
- metadata 声明“可能存在的 action”，projection 返回“当前确实允许的 action”；projection 的 action 必须是 metadata actions 的子集；
- Workflow 必须拒绝或幂等处理过期状态、错误节点、重复 operation ID 与已关闭 Run 上的 Signal；不能只依赖 org 的 projection 快照，因为查询与 Signal 之间存在竞态；
- timeout 必须由 Workflow deterministic Timer 实现，org 不在外部另起业务 timer；late action 的最终接受/拒绝以 Workflow state 为准；
- 同一 Run 可有多个并行 manual nodes；allowed action 必须带 `nodeID`，避免同名业务阶段混淆。

### Action Signal envelope

org 发送给 Worker 的内部 Signal payload 推荐固定为：

```json
{
  "operationId": "client-stable-id",
  "nodeId": "manager-approval",
  "action": "approve",
  "input": {},
  "principal": {
    "id": "principal-id",
    "tenantId": "authenticated-tenant-id"
  },
  "requestId": "gateway-request-id",
  "occurredAt": "server timestamp"
}
```

该 envelope 由 org 服务端构造，客户端不能提交或覆盖 principal、tenantId、requestId、signalName。Workflow 可以把审计上下文写入 deterministic state，但不得把认证信息当作绕过 Gateway 的独立安全凭证。

## Semantic projection contract

每个 Workflow contract 必须声明一个 projection query；默认名称可以是 `org_projection`。Worker 负责注册 query handler，并从 Workflow 的 deterministic business state 生成 projection。

```text
WorkflowProjection {
  contractVersion
  workflowName
  workerVersion
  runStatus
  nodes[] {
    id
    label
    status
    attempt?
    startedAt?
    completedAt?
    blockReason?
    failure? {
      code
      message
      retryable
    }
  }
  currentNodeIDs[]
  allowedActions[] {
    nodeID
    name
    label
  }
  updatedAt
}
```

### Node status enum

最低支持：

```text
pending
running
waiting-for-user
completed
failed
canceled
skipped
timed-out
```

语义：

- `pending`：依赖尚未满足或 Workflow 尚未调度该节点；
- `running`：节点正在由 Workflow 执行；并行分支可以同时有多个 running node；
- `waiting-for-user`：Workflow 已进入 Signal wait，至少有一个声明且当前允许的 action；
- `completed`：该语义节点成功完成；
- `failed`：节点进入业务失败终态；
- `canceled`：Run/分支取消导致节点结束；
- `skipped`：静态节点在本次路径不执行，适用于 optional/conditional semantic path；
- `timed-out`：节点自身 retry/interaction timeout 策略结束。

`currentNodeIDs` 是集合而非单值，以表达并行 running/waiting nodes。`blockReason` 放在具体节点上；Run-level block summary 可以由 org 对 projection 做安全聚合，但不能从 Temporal history 推导。

### org 对 projection 的验证

org 在返回或保存 projection snapshot 前必须验证：

1. projection contract version 受支持；
2. `workflowName` 与启动的 Workflow contract 一致；
3. `workerVersion` 与 Run 的 selected version 一致；
4. 每个 metadata node 在 projection 中恰好出现一次；禁止 unknown/duplicate node；
5. label 可以由 metadata 覆盖，不能让 projection 注入未声明的产品结构；
6. status 属于 enum；currentNodeIDs 只能引用 projection node；
7. allowed action 引用已声明 manual node/action，且对应 node 当前为 `waiting-for-user`；
8. projection 不包含 Signal name、Temporal ID/Task Queue、credentials、raw stack trace 或 secret value；
9. projection 不合法时返回稳定的 `invalid_worker_projection`，记录 tenant-scoped Audit/telemetry，并停止转发 action；不能猜测或自动修补业务状态。

org 可以验证结构一致性，但不能判断 Worker 的业务结论是否真实，也不能仅凭依赖图断言节点应该处于何种状态。状态权威仍是 Workflow 的 deterministic business state。

## 未来 Application/API contract（仅文档）

本规格不实现 HTTP handler。未来 public semantics：

### 读取 Run

```http
GET /workers/{workerName}/runs/{runID}
```

返回 Run、selected WorkerVersion description、validated semantic projection、deployment health 摘要；不返回 Temporal Workflow ID、Run ID、Task Queue、Worker Deployment、Signal name 或 credential。

### 发起业务 action

```http
POST /workers/{workerName}/runs/{runID}/actions/{actionName}
Idempotency-Key: <operationID>
```

```json
{
  "nodeId": "manager-approval",
  "input": {
    "comment": "approved"
  }
}
```

处理顺序：

```text
authenticate
  -> derive Tenant
  -> tenant-qualified Run lookup
  -> load Run's exact WorkerVersion metadata
  -> authorize declared requiredPermission
  -> validate current projection and action membership
  -> validate input JSON Schema
  -> atomically reserve action operationID
  -> send declared Signal with server-built envelope
  -> record outcome / uncertain delivery state
  -> append tenant-scoped Audit
```

禁止客户端提交 `tenantId`、`scope`、`signalName`、Temporal IDs、principal 或 arbitrary permission。

## 重复操作与不确定结果

- 每次 action request 必须有稳定 operation ID；正常重试复用同一个 ID；
- org 的唯一键至少包含 `(tenantID, runID, nodeID, actionName, operationID)`；
- 相同 operation ID + 相同 canonical payload 重放返回已有 outcome，不重复发送；相同 ID + 不同 payload 返回 conflict；
- org 在 Signal 已被 Temporal 接受、但尚未来得及持久化 completion 时可能遇到 ambiguous outcome。因此 Workflow 必须按 operation ID 去重；不能声称 action exactly once；
- operation record 至少区分 `reserved | delivered | accepted-by-workflow | rejected-by-workflow | delivery-unknown`；`accepted-by-workflow` 可通过后续 projection/ack state 对账，而不是假设 Signal RPC success 等于业务接受；
- reconciler 处理悬挂 reservation 与 delivery-unknown，但不得在无法证明安全时生成新的 operation ID 重发。

## Worker 重启、恢复与版本

- Worker Pod 重启不应丢失并行分支或人工等待；Temporal replay 从 Event History 恢复 Workflow deterministic state；
- projection query handler 必须仅读取已由 Workflow event processing 重建的 deterministic state；不得读取进程本地缓存、系统时间或外部服务来补状态；
- manual Signal 可能在 Worker 不在线时由 Temporal 持久化，Worker 恢复后处理；
- long-running Workflow 默认 Pinned，包含 manual wait 时旧 WorkerVersion 必须保持可用，直到 Run closed 或经过授权 migration；
- description PATCH 不改变 metadata、Signal contract、Build ID 或运行状态；Run 始终使用其 selected WorkerVersion contract。

## 超时、取消与补偿

- Activity timeout/retry 由 Workflow/Activity options 实现，并与 metadata 一致；
- manual timeout 由 deterministic Timer 实现；timeout outcome 必须在 metadata 中声明并反映为 `timed-out`、`skipped`、Run failure/cancel 或后续 default path；
- Run cancel 仍通过 org Gateway authorization；Workflow 应监听 cancellation，并按 metadata/业务代码执行必要补偿；
- cancel 后的 late action 返回稳定 conflict/not-allowed，普通用户不能获知其他 Tenant Run 是否存在；
- compensation 本身是 write Activity 时，同样必须具备稳定 idempotency/reconciliation contract；
- org 不通过修改 projection 伪造取消或补偿完成。

## 权限与审计

### 权限

建议基础权限：

```text
run:read
run:start
run:cancel
run:action:<catalog-action>
```

metadata action 的 `requiredPermission` 必须映射到管理员预先允许的 catalog；发布 WorkerVersion 不能凭 metadata 创建新的 platform role/permission。org 对每次 action 在当前 Tenant、Run、WorkerVersion 与 node/action 上重新授权。

Workflow 仍必须检查业务状态，但不负责替代 org 的终端用户 authorization。终端用户不获得 Temporal client/Worker credential。

### Audit

至少记录：

- Tenant ID/slug、principal、authentication method、request ID；
- Worker name、WorkerVersion、Run opaque ID、node ID、public action name；
- required permission、authorization result；
- schema validation outcome；
- operation ID、payload digest（不复制敏感 payload）；
- projection revision/digest used for precondition；
- Signal delivery result或 delivery-unknown；
- idempotent replay、Workflow acceptance/rejection/timeout/cancel outcome；
- timestamp、stable error class；
- 不记录 signal payload 中的 secret value、Temporal credential、raw history 或 Kubernetes secret。

authorization denial、schema rejection、stale action、duplicate/conflicting operation ID 与 adapter failure 都必须审计。

## 验收场景

1. metadata 可表达两个并行 root/branch 节点与一个多依赖 join；cycle、unknown dependency、duplicate ID 在部署前被拒绝。
2. org 保存并返回静态 contract，但没有任何代码根据 DAG metadata schedule Activity 或推进节点。
3. projection 可同时返回多个 `running` / `waiting-for-user` current nodes，并逐节点返回完整 status。
4. projection 缺节点、多节点、含 unknown node/status/action、版本不符或泄漏 Temporal identifier 时被拒绝为 `invalid_worker_projection`。
5. manual node 声明 public action、internal Signal、input schema、permission 与 timeout；普通 view 只显示 public action。
6. 未授权 action、schema invalid action、错误 Tenant、错误 Run、错误 node、非 waiting 状态均在发送 Signal 前失败并审计。
7. projection 查询后状态发生变化时，Workflow 能拒绝 stale Signal；org 不把旧 projection 当作最终业务锁。
8. 相同 operation ID + payload 的重复 action 不产生第二个业务效果；不同 payload 冲突；ambiguous delivery 不宣称 exactly once。
9. Worker crash/restart 后并行分支、manual wait、已处理 operation ID 与 projection 可由 Temporal replay 恢复。
10. manual timeout、Run cancel、Activity failure 与 compensation 都产生声明范围内的节点/Run 状态与 Audit。
11. write Activity 在 external success / Worker crash / completion 未记录场景中不重复外部效果，或按声明 reconciliation/compensation 进入可识别状态。
12. Current 与显式历史 Pinned WorkerVersion 对各自 metadata/projection contract 正确工作，不因新版发布破坏等待中的旧 Run。
13. 双 Tenant 同名 Worker/Workflow/action 仍无数据、operation ID、Signal 或 Audit 串扰。

## 需要用户确认的决定

### 1. DAG 是否只支持静态节点集合

推荐 MVP：metadata 是静态 semantic DAG；条件分支使用固定节点 + `skipped` 表达，不支持运行时生成未知 node ID。动态 fan-out 可以由 Worker 内部执行，但聚合到预先声明的 semantic node。若需要把每个动态 item 展示为独立节点，需要另定义 bounded dynamic-node contract、ID 和 projection size limit。

### 2. Join policy

推荐 MVP 只支持 `all`。`any`、quorum、first-success 会引入未完成分支取消、失败聚合与 projection 语义，需要后续单独扩展。是否接受此限制？

### 3. Manual description 与 block reason

推荐 metadata 提供 fallback block reason，projection 必须提供当前业务 block reason；org 对普通用户展示 projection 值并做长度/敏感字段限制。是否允许 Worker 返回任意动态文本，还是只允许 code + metadata 文案映射？更安全的方案是 `blockReasonCode + safeParameters`。

### 4. Action operation ID

推荐所有 manual action 强制 `Idempotency-Key`，由调用方在重试间稳定复用；org 与 Workflow 双层去重。是否允许浏览器在用户点击时由前端生成，还是必须由 Gateway 创建并返回后再提交？推荐 Gateway 提供/确认 ID，减少客户端错误复用。

### 5. Permission granularity

推荐 action 引用 platform-managed permission catalog，例如 `run:action:approve-order`，发布 metadata 只能引用、不能创建。需要确认 catalog 是全平台固定、按 Tenant admin 配置，还是 MVP 只支持统一 `run:action`。

### 6. Description PATCH 与 contract revision 的关系

WorkerVersion description 可独立 PATCH，但 DAG metadata 不可变。推荐 projection validation 绑定 immutable metadata digest，而不是可变 description revision。需要确认 Run view 是否展示 description 的最新文本，还是启动时 snapshot；推荐展示最新版本说明，同时 Audit 保留当时 revision。

### 7. Signal delivery acknowledgement

推荐 MVP 以 operation record + Workflow projection/ack field 对账，区分 RPC delivered 与 business accepted。是否要求每个 action 都在 projection 中暴露最近 ack，还是增加一个专用 query？推荐 projection 暴露 bounded `actionOutcomes`，避免再引入 Temporal-facing endpoint。

### 8. Timeout outcomes

推荐先支持 `fail | cancel | skip`，暂不支持 `continue-with-default`，因为 default payload/schema 会扩大 contract。需要确认 manual timeout 的 MVP 枚举。

用户确认这些决定后，再按 SDD → TDD 顺序实现 metadata validation、projection validation、action application service、idempotency ledger/reconciler、sample 与真实 E2E。UI 和 HTTP handler 继续推迟。
