# Org SDK：动态 Workflow 与受控 Temporal Go runtime

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product isolation is a Tenant; the underlying resources are the shared platform Temporal Namespace and platform Kubernetes Namespace.

## 状态

**Approved — implementation authorized on 2026-08-01.**

本文件只定义 Org SDK 的 authoring model、动态 semantic graph、projection、Activity/action contract、manifest、安全边界与版本兼容性。它不授权新增 SDK 代码、测试、generator、sample、control-plane adapter、UI 或 HTTP handler，也不授权 commit/push。

`005-interactive-parallel-dag-contract.md` 仍是 Draft。本规格保留 005 的产品目标，但修正两个核心假设：

1. production 用户不直接使用 Temporal Go SDK，而通过 Org SDK 编写可包含运行时分支与 fan-out 的确定性 Workflow；
2. semantic DAG 不是必须在发布时完全静态展开。manifest 声明可用的 node/action/Activity templates、schema、policy 与 bounds，SDK 在 Workflow 实际执行时维护当前 Run 的动态 graph projection。

005 与本规格都获批后，实施前应把重复 contract 收敛为一个版本化 schema。

## 核心决策

Org SDK 是 **Temporal Go SDK 之上的受控 Go authoring/runtime adapter**：

```text
用户声明业务步骤、依赖/并行、运行时分支、输入输出与等待动作
                              |
                              v
                 Org SDK Workflow program
                   /                     \
                  v                       v
      template/policy manifest       Worker-side runtime adapter
                  |                       |
                  v                       v
       org control-plane              Temporal Go SDK
       admission/action             durable execution
```

Org SDK 不是 source transformer、AI code generator，也不会把任意 Go 程序事后转换成 Temporal Workflow。用户从一开始就使用受控 `orgsdk.WorkflowContext` 与 SDK primitives；SDK runtime 才在 Worker 内部调用 Temporal Go SDK。

Temporal determinism、replay、Activity retry、Signal persistence、Timer、cancellation 与 Worker Versioning 约束仍然存在。SDK 缩小易错面并维护 contract，不能改变底层语义。

## 产品与执行边界

- org control plane **不执行业务 DAG**。它保存 manifest、校验 Worker projection、授权 action、部署 Worker 和管理版本。
- Org SDK runtime 在用户 Worker 的 Temporal Workflow 内执行依赖、if/else、fan-out、join、wait、resume 与 projection transition。
- Workflow code 不得直接调用外部服务。网络、数据库、文件、消息系统及其他外部 I/O 只能放在 Activity。
- Workflow 可以读取已经由 Temporal 记录的 Activity result，并基于该 result 执行 deterministic if/else、选择后续步骤或生成 fan-out items。
- UI/API 只消费 SDK 返回的 semantic projection；禁止从 Temporal Event History 猜测 DAG、节点状态或业务依赖。
- Activity attempt telemetry、security Audit、semantic projection 与 Temporal diagnostics 是不同来源，不得混成一个未经标注的“事实”。

## 目标

1. 用户主要表达业务步骤、依赖、并行、运行时选择、输入输出和可交互等待。
2. SDK 在确定性 Workflow state 中持续维护动态 semantic graph 与逐节点状态。
3. DAG template manifest、runtime projection 与实际 SDK primitives 使用同一份 typed Definition，避免 metadata 双写。
4. 所有 Activity 具有稳定 runtime identity、显式 retry/timeout 与 side-effect policy。
5. `WaitForAction` 等 idle node 使用 durable Signal wait，不占用 Activity worker。
6. WorkerVersion 发布时携带可校验、与镜像及 SDK runtime 绑定的 canonical manifest。
7. 长期 Pinned Run 在 Worker/SDK 升级后仍可 replay 和恢复。

## 非目标

- 让 control plane 根据 manifest 推进节点；
- 要求发布时枚举所有 runtime fan-out node；
- 用普通 Activity 阻塞等待用户 Signal；
- 自动把 Workflow 中的外部调用转换为 Activity；
- 承诺 exactly-once external effects；
- 允许终端用户直连 Temporal 或提交 internal Signal name；
- 由 org 构建或发布用户 OCI image。

## 核心概念

### Workflow Definition

发布时可冻结、可生成 manifest 的 contract，包含：

- Workflow name 与 typed input/output；
- Activity templates、action templates、semantic node templates；
- schema、retry/timeout、side-effect、idempotency/reconciliation/compensation policy；
- dynamic node ID derivation version；
- fan-out、runtime node count 与 projection size bounds；
- Org SDK module/runtime protocol/manifest versions；
- 用户提供的受控 Workflow function。

Definition 不必列出某次 Run 的完整 DAG 或所有 dependency edges。

### Runtime semantic graph

某个 Workflow Run 根据实际 input、已记录的 Activity result 与已接受 action payload，在 deterministic execution 中创建的 node/edge 集合。它可以因 if/else、dynamic selection 与 fan-out 在不同 Run 之间不同。

### Projection event 与 snapshot

SDK 在 Workflow deterministic state 中追加 graph events，并同步 materialize 当前 snapshot。UI 读取 snapshot；control plane 不解析 Event History构造这些 events。

## 用户编程模型

以下是意图示例，不是最终 Go API：

```go
var BuildPlan = orgsdk.DefineActivity[Input, Plan](
	"build-plan",
	BuildPlanActivity,
	orgsdk.ReadOnly(),
	orgsdk.Retry(standardRetry),
)

var ExecuteBranch = orgsdk.DefineActivity[BranchInput, BranchResult](
	"execute-branch",
	ExecuteBranchActivity,
	orgsdk.NoSideEffect(),
	orgsdk.Retry(standardRetry),
)

var ConfirmStart = orgsdk.DefineAction[ConfirmInput](
	"confirm",
	orgsdk.Permission("run:action:confirm"),
)

var Definition = orgsdk.DefineWorkflow[Input, Output](
	"parallel-confirmation",
	func(ctx orgsdk.WorkflowContext, in Input) (Output, error) {
		confirmation, err := ctx.AwaitConfirmation(
			"approval-gate",
			ConfirmStart,
		)
		if err != nil {
			return Output{}, err
		}

		plan, err := ctx.Activity("build-plan", BuildPlan, in)
		if err != nil {
			return Output{}, err
		}

		var branches []orgsdk.Future[BranchResult]
		for _, item := range plan.Branches {
			branches = append(branches, ctx.Activity(
				"branch/"+item.StableKey,
				ExecuteBranch,
				BranchInput{Plan: plan, Item: item, Confirmation: confirmation},
			))
		}

		results, err := ctx.Join("finalize", branches...)
		return MergeOutput(plan, results), err
	},
)
```

关键约束：

- 用户写的是受控 Org SDK Workflow function，不获得 raw `workflow.Context`。
- `ctx.Activity` 是唯一调度外部 I/O 工作的入口；Workflow function 只做 deterministic control flow 与纯数据 mapping。
- `plan` 是已记录的 Activity result。基于 `plan.Branches` 做 if/else 或 fan-out 是合法的 deterministic decision。
- 用户必须为重复 template occurrence 提供 stable semantic key；SDK 不能用 wall clock、random value 或不稳定 map iteration 派生 node identity。
- `MergeOutput` 等 replay-time callback 必须是无外部 I/O 的 deterministic pure function。
- SDK validation 无法自动把违规网络调用变成 Activity；production contract、lint、review 和 runtime admission 共同约束。

## 动态 graph 构造规则

### Node template 与 runtime node

manifest 声明 template，例如：

```text
templateID
type                  activity | wait-for-action | semantic
label template
input/output schema
allowed actions?
Activity policy?
runtime cardinality   singleton | repeated
maxInstances?
```

Workflow 执行时，SDK primitive 创建 runtime node。一个 runtime node 至少包含：

```text
runtimeNodeID
templateID
occurrenceKey
dependencies[]
status
createdAt
startedAt?
completedAt?
skip/failure/wait reason?
```

### if/else 与 skipped

- `ctx.If` / `ctx.Switch` 可以根据 Workflow input、已记录 Activity result 或已接受 action payload选择路径。
- 如果 alternatives 在 Definition 中被显式声明，SDK 为未选择的 candidate 创建 `skipped` node/event，使 UI 能显示“该路径未执行”。
- 对 data-driven fan-out 中根本不存在的 item，SDK不创建 phantom node，也不把它报告为 `skipped`。
- 已创建 node 的 dependency 在进入 `running` 或 `waiting-for-user` 后冻结；禁止事后改边导致 projection 重写历史。

### dynamic fan-out

- fan-out collection 必须来自 Workflow input、已记录 Activity result、accepted action input 或其他 deterministic state。
- 每个 item 必须提供 stable、唯一、可 canonicalize 的 business key。
- SDK 在 schedule 任一 branch 前按 canonical rule 验证 duplicate/invalid key 与 bounds。
- 分支可以引用同一 Activity template，但每个 runtime node/Activity ID 不同。
- join 可以等待本次 fan-out 实际创建的全部 branch，或等待 Definition 允许的显式 policy；首版推荐仅 `all`。
- manifest 必须声明 `maxInstancesPerFanOut`、`maxRuntimeNodes` 与 `maxProjectionBytes`，防止无界 projection/history growth。

## 动态节点稳定 ID

推荐 ID 模型：

```text
runtimeNodeID = <templateID>/<encodedOccurrencePath>/<hash>
hash = H(workflowContractID, parentRuntimeNodeID, templateID, canonicalOccurrenceKey)
```

- singleton node 使用 reserved occurrence key `singleton`；
- repeated/fan-out node 强制用户提供 business key；
- occurrence path 来自已持久化的 parent runtime node 与 key，不来自 process-local counter；
- 同一 Run replay 必须生成相同 ID；不同 branch key 必须生成不同 ID；
- retry 使用同一 runtime node 与 Activity ID，不创建新 node；
- 不允许用户直接提供完整 raw ID 或覆盖 hash；
- canonicalization 与 hash version 写入 manifest，升级时不得静默改变。

Activity ID 从 runtime node ID 做 domain-separated derivation。SDK 用 canonical Workflow identity、stable Activity ID 与可选 business key 派生 write idempotency material。downstream 是否正确去重仍由 Activity 作者负责。

## Dynamic projection event model

SDK 内部维护 append-only、deterministic、单调 sequence 的 projection events：

```text
GraphInitialized
NodeCreated
DependencyAdded
NodeStatusChanged
NodeSkipped
ActionOffered
ActionWithdrawn
ActionOutcomeRecorded
GraphCompleted
GraphFailed
```

event 至少包含：

```text
sequence
type
runtimeNodeID?
templateID?
dependencyRuntimeNodeID?
fromStatus?
toStatus?
reasonCode?
safeParameters?
deterministicTimestamp
```

规则：

- sequence 由 Workflow deterministic state 分配；
- graph event 只能由 Org SDK primitive 产生，用户不能直接 append 任意 event；
- `NodeCreated` 初始状态通常为 `pending`；wait node 可以原子进入 `waiting-for-user`；
- dependency 必须在 node 开始前完成添加，SDK 对当前 runtime graph 做 cycle/self-edge 检查；
- status transition 由 SDK 状态机验证，terminal node 不可回到 running；
- SDK 同步 materialize snapshot，避免 UI 重放无限 event log；
- Workflow 内只保留 bounded recent events + current snapshot，完整产品 Audit 由控制面按其可信来源保存；
- 若使用 Continue-As-New，SDK 必须把 graph checkpoint、operation dedupe 与 projection revision 作为受控 state 传入新 execution，不得让 UI 看到节点倒退或 identity 改变。

public projection 推荐返回：

```text
contractVersion
manifestDigest
projectionRevision
workflowName
workerVersion
runStatus
nodes[] {
  runtimeNodeID
  templateID
  label
  dependencies[]
  status
  createdAt
  startedAt?
  completedAt?
  blockReasonCode?
  safeParameters?
  failure?
}
currentNodeIDs[]
allowedActions[] {
  runtimeNodeID
  actionName
  label
  inputSchemaRef
}
```

UI 使用该 snapshot 渲染本次 Run 的实际 DAG。它可以增量读取 SDK projection delta，但不能从 Temporal Event History、Activity logs 或 telemetry补节点/边。

## Projection 与 Activity 实际生命周期边界

### Workflow-side transition

SDK 在 deterministic Workflow execution 中：

1. `ctx.Activity` 创建 runtime node 与 dependencies；
2. schedule 前把 node 标为 `running`，生成 stable Activity ID 与 options；
3. Temporal 记录 Activity result，Workflow replay/advance 处理 Future 后，SDK 才把 node标为 `completed`；
4. retry exhausted failure 被 Workflow 处理后，SDK 把 node 标为 `failed`；
5. timeout/cancel 按 policy 标为 `timed-out` / `canceled` / `skipped`。

这些 deterministic transitions 是 semantic projection 的权威来源。

### Activity worker-side hooks

SDK Activity wrapper/interceptor 在每次实际 attempt 周围：

- 注入 stable Activity ID、idempotency key、trace/Run correlation 与非敏感 Audit context；
- 发出 attempt started/completed/failed、latency、heartbeat 与 retry classification telemetry；
- 校验 write handler 显式接收 idempotency/reconciliation context；
- 遮蔽 sensitive fields，禁止 credential 进入 telemetry。

`running` 的精确定义是：**Workflow 已 schedule node，且尚未在 deterministic state 中处理到 terminal outcome。** 它不证明此刻某个 Activity process 正在运行。

Activity 可能已完成外部写，但 Worker 在 Temporal 记录 completion 前 crash。此时 telemetry 可能显示成功，projection 仍是 `running`。系统必须保留 ambiguous outcome，依靠 stable idempotency/reconciliation；不得用 telemetry 强行把 projection 改成 `completed`。

## Activity policy

每个 Activity template 必须声明：

```text
sideEffect            none | read | write
retryPolicy
timeouts
idempotencyPolicy?    retryable write required unless reconciliation is used
reconciliationPolicy?
compensationPolicy?
sensitiveFields?
```

- `none` / `read` 仍须显式 retry/timeout；
- `write` 必须传播 stable idempotency key，或声明 reconciliation；
- 限制 retry 不会消除 ambiguous outcome；
- reconciliation/compensation 若访问外部系统，本身也是 write Activity；
- SDK 与 org control plane 都拒绝不完整 policy；
- 任何层都不得宣称 exactly-once external effects。

## Idle interaction node：WaitForAction

### 技术模型

`WaitForAction` 是 **Workflow 内的 durable idle state**，不是普通 Temporal Activity：

```go
result, err := ctx.WaitForAction(
	"review-input",
	orgsdk.Actions(Approve, Reject, RequestChanges),
	orgsdk.Timeout(24*time.Hour),
)
```

标准 `AwaitConfirmation` 是单一 confirm/cancel 语义的 convenience implementation：

```go
confirmation, err := ctx.AwaitConfirmation("approval-gate", ConfirmStart)
```

SDK 内部使用 Workflow Signal receiver、deterministic Timer 与可 replay state 持久化等待。等待期间：

- 不 schedule 一个阻塞 Activity；
- 不占用 Activity worker slot/thread；
- Worker Pod 下线后，Signal 仍由 Temporal durable execution 接收并在 Worker 恢复后处理；
- projection node 为 `waiting-for-user`，包含 block reason 与当前 allowed actions；
- action完成、timeout 或 cancel 后，node进入 terminal status，并由用户 Workflow 决定后续路径。

普通 Activity 不是 Signal receiver。让 Activity 阻塞等待确认会占用 Worker、破坏可恢复路由语义，也无法作为 org Gateway 面向 Workflow 的受控 action contract，因此明确禁止。

### 自定义等待语义

用户可以声明：

- 一个或多个 action name/label；
- typed input 与 JSON Schema；
- required permission reference；
- block reason code 与 safe parameters；
- timeout/cancel policy；
- deterministic action transition handler；
- 是否允许 action 后继续等待其他 action。

用户不能声明 internal Signal name、Signal envelope、Tenant/principal 或 raw Temporal routing identifiers。SDK 为所有 action 使用 reserved Signal protocol。

### Action/Signal 生命周期

推荐状态：

```text
SDK Workflow node:
  pending -> waiting-for-user -> completed | timed-out | canceled | failed

org operation record:
  reserved -> delivered | delivery-unknown
           -> accepted-by-workflow | rejected-by-workflow | duplicate | expired
```

处理顺序：

```text
UI/API action
  -> org Gateway authenticate and derive Tenant
  -> tenant-qualified Run + exact WorkerVersion lookup
  -> authorize required permission
  -> validate current projection/action membership
  -> validate input schema
  -> reserve operation ID
  -> send reserved Signal envelope
  -> SDK Signal handler deduplicates and revalidates current wait state
  -> record accepted/rejected outcome in deterministic state
  -> projection withdraws/offers actions and advances node
  -> org reconciles operation record and appends Tenant-scoped Audit
```

Signal RPC success 只表示 delivery，不等于业务接受。query 与 Signal 之间可能竞态；SDK 必须拒绝 expired/stale action。相同 operation ID + canonical payload 是 idempotent replay，不同 payload 是 conflict。Worker restart 后 dedupe state 必须由 replay 恢复。

## Manifest contract 与动态 DAG

推荐 **code-first Definition + generated canonical manifest**：

1. 用户声明 Workflow function 与 node/Activity/action templates；
2. generator 做 template/schema/policy/bounds validation；
3. 生成 canonical manifest、schemas 与 embedded digest；
4. 用户 CI 构建 OCI image并附带 manifest digest/provenance；
5. Worker startup 验证 Definition、embedded manifest 与 runtime protocol；
6. WorkerVersion registration 提交 image digest、manifest、manifest digest 与 provenance；
7. org 不构建或发布 image。

manifest 至少包含：

```text
contractVersion
manifestDigest
projectionEventVersion
dynamicNodeIDVersion
sdk { moduleVersion, runtimeProtocolVersion }
workflows[] {
  name
  versioningBehavior
  inputSchema
  outputSchema
  nodeTemplates[]
  actionTemplates[]
  runtimeBounds
}
activityTemplates[] { schemas, sideEffect, retry/timeout, safety policies }
capabilities[]
```

manifest 声明“哪些 runtime graph 结构合法”，而不是预言每个 Run 的完整 graph。org 验证 projection 时要求：

- 每个 runtime node 引用已声明 template；
- ID 符合指定 derivation version；
- dependency 引用存在 node，且无 cycle；
- status/action transition 合法；
- fan-out 与 projection 未超 bounds；
- action、schema 与 permission reference 来自 manifest；
- projection 不含 credential、internal Signal、Temporal routing identifier 或 raw stack。

manifest 不包含 Tenant ID、`scope`、重复 Worker name、WorkerVersion description 或 environment endpoint。

### Runtime identity proof

推荐 SDK 注册 reserved internal `org.sdk/contract-probe` Workflow。org 在 Worker poller ready 后、WorkerVersion promotion 前，以该版本启动 probe并核对 manifest digest、SDK module/runtime protocol、capabilities 与 Worker Build ID。`org.sdk/*` prefix 由 SDK 保留，用户不可占用。不得使用 Temporal 自身保留的 `__*` Query prefix。

该 probe 能证明目标 Worker runtime 声明并加载对应 Definition，但在 org 不构建用户 image 的边界下，不能密码学证明 image 内没有 raw Temporal bypass。更强保证需要 signed build attestation、受控 builder、SBOM/static analysis 或 Worker-to-Temporal proxy，另写 supply-chain spec。

## SDK runtime 与 org control plane 执法矩阵

| 能力 | SDK runtime | org control plane |
|---|---|---|
| runtime graph | 按 deterministic execution 创建 node/edge | 验证 projection/manifest，不推进节点 |
| if/else/fan-out/join | 基于 input、recorded result、accepted action 执行 | 校验 bounds 与 contract |
| projection events/snapshot | 生成并 materialize | 校验、缓存并安全返回 |
| Activity ID | 按 runtime node/key 派生 | 验证 ID version，记录 correlation |
| Activity retry/timeout | 映射为 Temporal options | admission 检查 policy |
| write safety | 注入 idempotency/reconciliation context | 拒绝缺少 policy 的 manifest |
| external effect | 用户 Activity 执行 | 不执行、不承诺 exactly once |
| idle wait | Workflow Signal/Timer state | 不启动阻塞 Activity |
| action | dedupe、state revalidation、transition | auth、schema、ledger、Signal delivery、Audit |
| trace | Workflow/Activity context propagation | 创建可信 correlation、采集 telemetry |
| Tenant authorization | 不负责 | 唯一 user-plane enforcement point |
| manifest identity | startup/probe 自证 | registration、OCI/probe consistency check |
| version routing | 注册 SDK capabilities | Current/history/Pinned routing 与 lifecycle |

## Trace 与 Audit context

控制面创建不含 credential 的受控 context：Tenant/Worker/WorkerVersion/Run opaque refs、request ID、trace parent、发起 operation 的 principal ref 与 operation ID。SDK 把 immutable correlation fields 放入 Workflow deterministic state并传播到 Activity hooks。

- Worker 自报业务字段标为 worker-reported，不能覆盖控制面派生的 Tenant/principal；
- Activity telemetry 记录 attempt 事实；org security Audit 记录谁被授权执行什么；
- payload 默认只保存 schema outcome、digest 或 allowlisted field；
- dynamic node/action Audit 使用 runtime node ID + template ID，确保同一 fan-out 中可定位。

## Determinism 与 replay

- Workflow function、branch predicate、fan-out key function、merge、action transition 都会 replay，必须无外部 I/O、无 wall clock/random、无不稳定 map iteration、无进程全局可变状态。
- SDK 提供 deterministic time、version gate、stable ordering 与 testkit helper，不暴露 raw Temporal context。
- Activity result 在 Workflow 决策前已由 Temporal 记录，因此基于该 result 的 branch/fan-out 可 replay；Activity 内未返回的外部状态不能被 Workflow直接读取。
- 修改 predicate、template ID、dynamic key、dependency、Signal/action contract 或 ID derivation 可能破坏旧 history，必须受 SDK version gate 和 WorkerVersion policy 管理。
- Pinned 保证 Run 留在选定 WorkerVersion，但不能修复该 image 自身不兼容的代码变更。

## SDK versioning 与 backward compatibility

版本轴分开：

1. `sdkModuleVersion`：用户编译依赖版本；
2. `runtimeProtocolVersion`：Worker/control-plane handshake、projection/action protocol；
3. `contractVersion`：manifest schema；
4. `projectionEventVersion`：dynamic event/state machine schema；
5. `dynamicNodeIDVersion`：stable ID canonicalization/derivation。

规则：

- WorkerVersion 固定记录版本与 capability set；unknown major/required capability 在部署前拒绝；
- minor 只能增加可选字段，不得静默改变 event transition 或 ID derivation；
- SDK release 必须有 golden manifest、dynamic graph state-machine、old-history replay 与 N/N-1 compatibility tests；
- breaking branch/action/ID semantics 使用新 major和新 WorkerVersion；
- long-running Pinned Run 由原 image/SDK runtime 处理，直到关闭或授权 migration；
- deprecation 不能让仍有 Pinned Run 的 runtime 静默下线。

## Escape hatch policy

MVP 推荐禁止 production Workflow 使用 raw Temporal Go SDK：

- user-facing SDK 不返回 raw `workflow.Context`、client、Signal channel 或 interceptor；
- 用户不可注册额外 Workflow/Signal/Query，也不可覆盖 `org.sdk/*`；
- Activity 可使用普通 Go client 做外部 I/O，但受 policy/hooks 约束；
- 无法表达的需求优先新增 Org SDK primitive，而不是开放 raw escape hatch。

org 当前不构建用户 image，仅靠 Go API 无法阻止恶意 image 私自链接 raw SDK。因此这里的“禁止”是 production contract + admission/probe + trusted Worker policy，不是假装已有 sandbox。针对不可信 Worker 的强制保证需要 build attestation 或 Worker-to-Temporal proxy。

## 高级 parallel-confirmation Sample 的未来方向

未来 `samples/parallel-confirmation` 只作为 Org SDK consumer：

```text
AwaitConfirmation approval gate
  -> BuildPlan Activity
  -> inspect recorded Plan result in Workflow
  -> runtime fan-out two branch nodes with stable keys
  -> join actual branch set
  -> finalize
```

- Sample 用户只实现 `BuildPlan`、两个 branch Activity 与 pure merge；
- `BuildPlan` result 决定实际 branch input/selection，SDK 在运行时创建 node/edge；
- approval 使用标准 `AwaitConfirmation`，不是阻塞 Activity；
- SDK 自动生成/维护 dynamic projection、Signal protocol、stable IDs 与 manifest；
- Sample 不出现 raw Temporal context、手写 projection、Signal handler 或 metadata JSON；
- local test 使用 Org SDK testkit；production-like E2E 必须通过 org Gateway action，不直连 Temporal模拟终端用户；
- UI/HTTP handler 继续等待单独批准。

该 Sample 的独立 SDD 要等本规格与 005 获批、动态 contract 稳定后再写。

## Dynamic decision Sample

第三个 Sample 固定为 `samples/dynamic-decision`，只使用 Org SDK，演示最小 if/else 路径：

```text
DetermineRoute Activity
  -> route == "concise" ? ConciseBranch : DetailedBranch
  -> 未选择的 candidate branch = skipped
  -> finalize
```

- 使用中性的“根据输入选择简洁或详细处理方式”叙事，不连接外部系统；
- `DetermineRoute` 是起始 Activity，其 recorded result 是 Workflow 选择路径的唯一依据；
- 两个 candidate branch 都在 Definition/manifest 中声明，但每个 Run 只执行一个；
- SDK 为未选择分支创建 runtime node并标记 `skipped`，UI 可同时看到 selected 与 skipped branch；
- selected branch 完成后进入共同 `finalize` semantic node；
- Sample 只提供 typed Activity 与 pure output merge，不手写 raw Temporal Workflow、projection、Signal handler 或 metadata JSON；
- 单测必须分别覆盖两个 route、非法 route、skipped projection、stable runtime node ID 和 terminal result；
- production-like E2E 经 org 部署并读取 SDK projection，不从 Event History 推断 branch。

## TDD 与最低验收（获批后）

实施顺序：

1. template/manifest/policy/bounds validation；
2. projection event state machine 与 snapshot materialization；
3. deterministic if/else、skipped candidate 与 Activity-result-driven fan-out；
4. dynamic stable ID、duplicate key、replay 与 bounds tests；
5. Activity hooks、write idempotency/reconciliation 与 ambiguous crash test；
6. `WaitForAction` / `AwaitConfirmation` waiting、Signal dedupe、timeout/cancel/restart tests；
7. action ledger/Signal lifecycle integration；
8. manifest embed/probe 与 version compatibility；
9. SDK testkit；
10. 最后编写高级 Sample 与真实 kind/Temporal E2E。

最低验收：

- Activity result 的不同值产生不同且可 replay 的 runtime graph；
- if/else 未选 candidate 为 `skipped`，dynamic fan-out 不生成 phantom node；
- 两个 fan-out branch 在等待任一结果前都已 schedule，projection 同时显示两个 `running` node；
- node/edge/status/action 只能通过 SDK event state machine变化，UI 不解析 Event History；
- 同一 input/history replay 得到相同 runtime node ID、dependency、projection revision；
- `WaitForAction` 在等待期间没有 pending/running Activity Task，Worker 重启后仍可收到受控 Signal；
- unauthorized/schema-invalid/stale action 在正确层被拒绝并审计；duplicate operation ID 不重复推进；
- `running` 不被误述为 Activity attempt 正在执行；
- external success / Worker crash / completion missing 不产生重复效果，或进入 reconciliation state；
- old Pinned Run 继续由兼容 SDK runtime replay；
- 双 Tenant 同名 Worker 不共享 graph、action ledger、runtime name 或 Audit。

## 已批准的设计选择

### 1. 动态 authoring API

采用受控 `func(ctx orgsdk.WorkflowContext, ...)`，使用 `ctx.Activity`、`ctx.If/Switch`、`ctx.FanOut/Join` 与 `ctx.WaitForAction`；不暴露 raw Temporal context。

### 2. Dynamic projection event model

Workflow 内维护 append-only deterministic events + materialized snapshot。snapshot 是稳定 public contract；events 是 SDK 内部版本化实现，后续可以按 revision 暴露 bounded delta，但不要求 UI 重放完整 events。

### 3. 动态节点 stable ID

repeated/fan-out node 强制显式 stable business key，并用 parent runtime ID + template ID + canonical key 派生不可覆盖的 runtime ID。production 不提供 deterministic counter 后备。

### 4. Action/Signal lifecycle acknowledgement

区分 Gateway `delivered/delivery-unknown` 与 Workflow `accepted/rejected/duplicate/expired`，由 projection 的 bounded action outcome + org ledger 对账。action service 先返回 delivered/pending 语义，再异步对账 Workflow acceptance，避免把业务接受误当同步 RPC。

### 5. Dynamic graph bounds 与 Continue-As-New

manifest 强制 `maxInstancesPerFanOut`、`maxRuntimeNodes`、`maxProjectionBytes`；超限时产生稳定 Workflow failure，不静默截断 DAG。Continue-As-New 传递受控 graph checkpoint；首版保持 checkpoint snapshot，不承诺无限历史 events。

实施同时同步 005 的静态 projection 假设，所有新增行为继续严格按 TDD 推进。
