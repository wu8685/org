# org Console UI 与 HTTP/JSON API

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product identity and authorization use Tenant; infrastructure uses one shared platform Temporal Namespace and one shared platform Kubernetes Namespace. UI 不把 Tenant 映射为任何底层资源边界。

## 状态

**Approved — implementation authorized by the user on 2026-08-01.**

### Approved amendment: publish without manifest upload

**Approved for implementation.** [`012-worker-bootstrap-registration.md`](012-worker-bootstrap-registration.md) is authoritative where older paragraphs below contain `contractArtifact` or generated-manifest file selection.

012 supersedes the `contractArtifact` publish input and generated-manifest file picker described below:

- publish form/API只接收version、description、immutable image digest、versionConfig与runtime；repository、branch、commit、CI reference等provenance不由用户填写；
- UI不要求选择、上传或粘贴manifest，也不计算/提交manifest digest；
- publish返回`202 Accepted`，WorkerVersion detail进入durable pending verification；
- timeline分别展示candidate deployment、`SDK registration`、Worker polling、`contract verification`与promotion；
- registration前contract区域显示“等待Worker通过Org SDK注册”；registration accepted后从control-plane read model只读展示contract；
- SDK registration rejected、expired、image mismatch、protocol mismatch、poller timeout与probe mismatch使用不同安全状态，不泄露bootstrap credential、Pod evidence或platform routing；
- refresh后从durable release/operation恢复，不依赖浏览器缓存或in-memory publish operation；
- optional generated JSON仅供CI/debug，Console没有离线manifest file输入。

本文件把用户提供的 `/Users/wu8685/Downloads/org-worker-console.html` 转化为正式产品与接口规格。参考文件 SHA-256 为：

```text
d34faa7e217f1e9b9396886fdcc8de5d4b3217bd01459214aecd7a7ffdbde0c7
```

用户已经确认以下方向：

- Tenant → Worker → WorkerVersion → Workflow → Run 信息架构；
- 参考 HTML 的简洁控制台视觉风格；
- SDK manifest 只读且经 contract probe 验证；
- Run DAG 由动态 semantic projection 渲染；
- 人工 action 必须经过 org Gateway；
- Routines 不属于当前 MVP。

本规格定义 UI、application read/write contract 与 HTTP/JSON presentation layer。实现须按 SDD/TDD 分纵向切片推进。

## 产品边界

Console 是 org control plane 的 Tenant-scoped 视图，不是 Temporal 或 Kubernetes 管理面：

```text
browser
  -> org same-origin HTTP/JSON API
       -> authenticated Tenant context
       -> control-plane application services
            -> org store / Kubernetes adapter / Temporal adapter

Worker semantic projection
  -> control plane validates identity, contract and bounds
  -> API returns validated Run read model
  -> UI renders runtime DAG
```

- 浏览器不接触 Temporal/Kubernetes credential、Task Queue、Worker Deployment、raw Signal、platform resource name或Event History。
- Temporal Web只作为具有 `diagnostics:read` 权限的高级诊断deep link。
- UI不执行业务 DAG，不根据manifest推进节点，也不在浏览器本地伪造Run状态。
- 任何业务事实必须标注来源：org record、Kubernetes health、Temporal execution、SDK semantic projection或action operation ledger。
- public API/UI/manifest继续禁止 `scope`；稳定边界是认证Tenant + `workerName`。

## MVP 范围

### 包含

- 当前 Tenant 总览与quota摘要；
- Worker列表、Worker详情、WorkerVersion列表与详情；
- 创建逻辑Worker并录入首个WorkerVersion，或为已有Worker录入新版本；
- 版本级description查看与revision-safe更新；
- read-only SDK manifest、workflow/action/schema与contract probe结果；
- 按Worker → WorkerVersion → Workflow浏览与触发；
- Current与显式历史版本选择；
- Run列表、Run详情、动态DAG、block reason、allowed actions与action outcome；
- Run cancel；
- 受权限保护的Temporal高级诊断deep link。

### 不包含

- Routines、cron、event trigger或任何自动触发配置；
- raw Temporal Signal/Query、Task Queue或Worker Deployment管理；
- Kubernetes resource editor、Pod shell/log viewer或Secret value展示；
- 在UI手写/修改SDK manifest、projection、workflow contract或action metadata；
- 通过Event History推断业务DAG；
- arbitrary JavaScript/custom renderer；
- org控制面为用户build或push image。

## 信息架构与路由

MVP sidebar只显示“总览 / Workers / Workflows / Runs”。参考HTML中的Routines入口、列表、create modal与Routine trigger文案全部移除，不显示disabled假数据。

| UI route | 页面 | 数据与操作 | 主要权限 |
|---|---|---|---|
| `/` | Tenant总览 | Tenant identity、quota、Worker/Run摘要 | `worker:read`, `run:read` |
| `/workers` | Worker列表 | 搜索、health筛选、创建/录入入口 | `worker:read`; mutation另验权限 |
| `/workers/{workerName}` | Worker详情 | immutable name、Current、versions、recent Runs | `worker:read`, `run:read` |
| `/workers/{workerName}/versions/{version}` | WorkerVersion详情 | description、image/source/runtime、health、probe、read-only contract、Pinned Runs | `worker:read` |
| `/workflows` | Workflow catalog | Worker → Version → Workflow层级、input schema、触发入口 | `worker:read`, `run:start` |
| `/workers/{workerName}/versions/{version}/workflows/{workflowName}` | Workflow详情/触发 | contract、schema、version选择、Run输入 | `worker:read`, `run:start` |
| `/runs` | Run列表 | worker/workflow/version/status筛选 | `run:read` |
| `/runs/{runId}` | Run详情 | execution + validated dynamic projection + action ledger + diagnostics | `run:read`; action/cancel另验权限 |

路由参数只是resource locator，不能决定Tenant。服务端始终从tenant-scoped认证主体派生Tenant，再在该Tenant内lookup；跨Tenant同名resource返回统一not-found，避免IDOR枚举。

### Tenant switcher

一个principal如属于多个Tenant，switcher先读取认证系统签发的membership列表。切换动作是**认证上下文交换**，不是业务请求中提交 `tenantId`：

1. 浏览器请求auth/session层为已验证membership签发新的tenant-scoped session；
2. 服务端验证principal确属目标Tenant；
3. 新session只携带一个Tenant ID/slug与其permission集合；
4. 页面清除上一Tenant cache、返回总览并重新查询；
5. 后续domain API忽略并拒绝body/query/header中的`tenantId`覆盖。

若首版认证只支持单一Tenant主体，switcher隐藏；不得用前端local state模拟Tenant隔离。

## 视觉与交互方向

参考HTML的视觉token作为基线而非逐像素复制：

- `#fafafa`页面背景、白色surface、`#111`正文、低对比border与`#2f6feb`主accent；
- desktop 约244px sidebar、sticky约66px topbar、最大内容宽1240px；
- 8/12/16px radius，低阴影、留白优先、Inter/system sans + mono技术字段；
- card/table/status pill/tab/modal沿用简洁、克制层级；
- success/warning/danger不只依赖颜色，始终带文本/图标；
- motion短且可关闭，尊重`prefers-reduced-motion`。

响应式基线：

- `>1020px`：sidebar + main，表格与详情双栏；
- `701–1020px`：收窄gutter、两栏降为单栏或可滚动区域；
- `<=700px`：sidebar改为可访问的drawer/top navigation，table转label-value card，modal全宽；
- DAG在窄屏使用可滚动分层图或结构化节点列表，绝不把并行关系错误压成一条伪串行箭头。

## 认证、授权与安全

- 所有mutation需要authenticated same-origin request、CSRF防护、request ID与Tenant-scoped audit。
- UI根据read model隐藏无权限按钮只是体验优化；服务端必须独立授权。
- resource body禁止`tenantId`、`tenantSlug`、`scope`、Task Queue、Worker Deployment、Workflow ID、Kubernetes name与Temporal credential。
- output可返回当前Tenant display identity，供用户确认上下文；不得返回其他Tenant数据。
- input、action payload、version config与error按size limit处理；页面不把未转义业务文本插入HTML。
- Secret只展示reference name/状态，不展示value；普通API不返回Pod env、Temporal History或platform credential。
- Temporal diagnostics URL只在`diagnostics:read`存在时返回，并由服务端生成；UI不自行拼接。

### 权限矩阵

| 能力 | 服务端permission |
|---|---|
| 读取Worker/WorkerVersion/Workflow contract | `worker:read` |
| 创建逻辑Worker | `worker:create` |
| 发布WorkerVersion | `worker:deploy` |
| 更新version description | `worker:version:update` |
| 启动Run | `run:start` |
| 读取Run/projection | `run:read` |
| 取消Run | `run:cancel` |
| 执行节点action | manifest中该action的`requiredPermission`，例如`run:action:confirm` |
| 打开高级诊断 | `diagnostics:read` |

## HTTP/JSON 通用约定

接口使用`/api/v1`、UTF-8 JSON、UTC RFC3339 timestamp与opaque cursor。响应包含`requestId`，列表返回`items`与可选`nextCursor`。

错误envelope：

```json
{
  "error": {
    "code": "projection_conflict",
    "message": "Run state changed; refresh before acting",
    "requestId": "req-...",
    "details": {}
  }
}
```

稳定错误码至少包含：`unauthenticated`、`permission_denied`、`not_found`、`conflict`、`validation_failed`、`quota_exceeded`、`contract_probe_failed`、`projection_invalid`、`projection_conflict`、`delivery_unknown`、`dependency_unavailable`。

- `400`：JSON/schema/field validation；
- `401/403`：认证/权限；
- `404`：Tenant-scoped resource不存在，包括cross-Tenant locator；
- `409`：revision/idempotency/stale projection冲突；
- `412`：`If-Match`失败；
- `422`：artifact/contract语义不合法；
- `429`：Tenant quota/rate limit；
- `202`：异步publish或action delivery尚未得到Workflow outcome；
- `503`：Temporal/Kubernetes等dependency不可用，不把它伪装成业务失败。

读接口支持`ETag`/`If-None-Match`。description与projection相关mutation使用`If-Match`。浏览器对active Run默认约2秒poll，对列表约10秒poll；页面不可见时暂停高频poll，并允许手动刷新。实现可在后续升级SSE，但MVP不依赖它。

## Application/API contract

### Session 与总览

| Method | Path | Contract |
|---|---|---|
| `GET` | `/api/v1/session` | 当前principal、当前Tenant、permissions；可选authorized Tenant memberships来自auth层 |
| `GET` | `/api/v1/overview` | quota usage、Worker state counts、Run state counts、recent Runs；全部当前Tenant限定 |

### Worker 与 WorkerVersion

| Method | Path | Contract |
|---|---|---|
| `GET` | `/api/v1/workers` | Tenant内Worker列表与Current/readiness摘要 |
| `POST` | `/api/v1/workers` | `{ "workerName": "..." }`；immutable，禁止scope/tenant字段 |
| `GET` | `/api/v1/workers/{workerName}` | Worker identity、Current指针、version摘要与recent Runs |
| `GET` | `/api/v1/workers/{workerName}/versions` | version列表 |
| `POST` | `/api/v1/workers/{workerName}/versions` | 发布命令，见下文；推荐`202`+operation/read URL |
| `GET` | `/api/v1/workers/{workerName}/versions/{version}` | version详情、health/probe/runtime、read-only contract；旧source字段仅兼容读取且UI不展示 |
| `PATCH` | `/api/v1/workers/{workerName}/versions/{version}/description` | `{ "description": "..." }` + `If-Match`;只更新description/revision |

### WorkerVersion 发布输入

UI可编辑字段：

```json
{
  "version": "2026.08.1",
  "description": "本版本做什么；创建时必填。",
  "image": "registry.example.com/worker@sha256:...",
  "versionConfig": {
    "region": "ap-southeast-1",
    "provider": { "secretRef": "provider-token" }
  },
  "runtime": {
    "cpu": "100m",
    "memory": "128Mi"
  }
}
```

关键边界：

- `versionConfig`与`runtime`是版本配置，可由用户编辑；Secret只用reference。
- publish body禁止manifest、metadata、contract、projection与manifest digest；HTTP客户端提交这些字段也必须被strict decoder拒绝。
- Org SDK在候选Worker startup从typed Definition构造canonical contract/digest，经bootstrap registration accepted后才开始polling；UI不参与contract生成或传输。
- registration accepted后UI只读展示server-stored contract、digest、SDK/runtime identity与后续probe结果。
- manifest不包含`scope`、Tenant、重复Worker name、version description或version config。
- `description`属于WorkerVersion且创建必填；PATCH只改变description和revision，不能改变image、runtime、manifest或Temporal Build ID。
- org不build/push image；只接受allowlisted registry中的`repository@sha256:<64 lowercase hex>` platform-specific digest。mutable tag、`tag@digest`、multi-arch index digest、observed image mismatch或probe mismatch发布失败。
- publish必须携带`X-CSRF-Token`与`Idempotency-Key`。CSRF token从同一认证session下的`GET /api/v1/session`读取；publish idempotency ledger及24小时默认retention以012的Approved clarification为准。

实现复用现有domain contract；`versionConfig`持久化与artifact ingestion若尚缺行为，必须先补独立测试，再扩展domain。UI不得先以local-only字段假装保存成功。

### WorkerVersion read model

Version详情分开显示：

1. **Release**：version、description、revision、immutable image digest、created/actor；
2. **Runtime config**：versionConfig、CPU/memory、Secret references；
3. **Deployment health**：Kubernetes ready、Worker polling、state/failure；
4. **Contract verification**：submitted manifest digest、canonical validation、probe observed digest、SDK module/runtime protocol、Build ID、`verifiedAt`、`verified | mismatch | unsupported | pending`；
5. **Read-only contract**：workflows、typed input/output schema、node templates、action schema/permission、Activity policy与runtime bounds；
6. **Retention**：Current标记与Pinned Run count。

“Ready”不能仅由Pod Ready推断。UI必须分别展示Kubernetes、Worker poller与contract probe；mismatch/unsupported不能以warning继续标成Ready。

### Workflow 与 Run

| Method | Path | Contract |
|---|---|---|
| `GET` | `/api/v1/workflows` | 当前Tenant的Worker → Version → Workflow只读层级，可按Current/ready过滤 |
| `POST` | `/api/v1/workers/{workerName}/workflows/{workflowName}/runs` | 启动Run；可选`workerVersion`，省略用Current；body含`input` |
| `GET` | `/api/v1/runs` | filter: workerName/workflow/workerVersion/status/cursor |
| `GET` | `/api/v1/runs/{runId}` | Run、selected WorkerVersion description、execution、validated semantic projection、action operation与可选diagnostics URL |
| `POST` | `/api/v1/runs/{runId}/cancel` | cooperative cancel，幂等；不声称撤销已发生外部效果 |

启动请求：

```http
POST /api/v1/workers/dynamic-decision-worker/workflows/DynamicDecisionWorkflow/runs
Idempotency-Key: user-generated-stable-key
Content-Type: application/json

{
  "workerVersion": "2026.08.1",
  "input": {"mode":"concise","subject":"release notes"}
}
```

- `workerVersion`省略时读取Current；显式历史ready版本使用Temporal Versioning Override并保持Pinned。
- 每次无idempotency key调用创建独立Workflow Execution；相同key + 相同Worker/Workflow返回已有Run。
- input按所选版本的只读schema验证。UI从schema生成基础form，同时保留结构化JSON模式；两者产生同一JSON，不允许修改schema本身。
- response和Run详情始终显示实际selected version与该version的description。

## Dynamic semantic projection renderer

Run详情只消费control plane已经验证的SDK projection：

```json
{
  "contractVersion": "org.worker/v1",
  "workflowName": "DynamicDecisionWorkflow",
  "workerVersion": "2026.08.1",
  "projectionRevision": 12,
  "runStatus": "running",
  "nodes": [
    {
      "runtimeNodeId": "concise-branch-...",
      "templateId": "concise-branch",
      "label": "Concise branch",
      "dependencies": ["determine-route-..."],
      "status": "completed",
      "reasonCode": "",
      "createdAt": "...",
      "startedAt": "...",
      "completedAt": "..."
    }
  ],
  "currentNodeIds": [],
  "allowedActions": [],
  "actionOutcomes": [],
  "recentEvents": []
}
```

Renderer规则：

- 节点数运行前未知；每个`runtimeNodeId`是UI identity，更新时保持位置/展开状态稳定。
- edge严格来自`dependencies`；布局只决定坐标，不创造或删除dependency。
- 支持多个root、并行current nodes、if/else `skipped`、data-driven repeated fan-out、multi-dependency join、waiting action与terminal状态。
- 状态全集：`pending | running | waiting-for-user | completed | failed | canceled | skipped | timed-out`；UI不自行创造`blocked`节点。未创建的future branch不显示，已创建pending节点可根据dependency显示等待原因。
- `currentNodeIds`高亮所有active节点，不假定单一current step。
- `reasonCode`显示为安全、可本地化的block/failure解释；未知code显示通用文案并保留code供诊断。
- `allowedActions`只附着到匹配runtime node；manifest提供schema与requiredPermission。
- topological layered layout按dependency计算；同层节点按`createdAt`、再按`runtimeNodeId`稳定排序。新增node时尽量保留既有node位置。
- 大图支持pan/zoom、fit、节点搜索与按status筛选；不能以固定absolute-position六节点DOM实现。
- 窄屏使用可访问的分层列表/邻接说明，明确每个节点的上游；不能用单一向下箭头把fork/join错误串行化。
- projection invalid、query失败或revision倒退时停止增量更新，显示明确contract error并提供request ID；不得降级解析Event History。

Run summary同时展示：Run ID、Worker/selected version、version description、Workflow、trigger=`manual | api`、execution status、projection status/revision、created/duration、block reason与allowed action count。Routine/event trigger在MVP不存在。

## 人工 action contract

UI点击action不是本地状态transition。流程固定为：

```text
validated projection allowedAction
  -> load manifest action schema + requiredPermission
  -> render accessible form
  -> generate/reuse Idempotency-Key
  -> POST org Gateway action API with projection If-Match
  -> show delivery state
  -> poll Run/action outcome until accepted/rejected/expired
  -> refresh validated projection
```

接口：

```http
POST /api/v1/runs/{runId}/nodes/{runtimeNodeId}/actions/{actionName}
Idempotency-Key: op-...
If-Match: "projection-r17"
Content-Type: application/json

{"input": {}}
```

服务端必须：

- 从authenticated principal派生Tenant并验证Run ownership；
- 根据stored WorkerVersion manifest查action、node template、input schema与requiredPermission；
- query并验证当前projection，核对node仍`waiting-for-user`且action仍allowed；
- canonicalize input、保存payload digest和operation reservation；
- 相同operation key + 相同payload返回同一operation，不重新推进；不同payload返回conflict；
- 发送reserved SDK Signal，不接受浏览器传raw Signal name；
- 记录Tenant ID、principal、Run/node/action/operation、delivery/outcome与request ID audit；
- 通过projection action outcome reconcile ledger。

response：

```json
{
  "operationId": "op-...",
  "state": "delivery-unknown",
  "runId": "inv-...",
  "runtimeNodeId": "approval-gate-...",
  "action": "confirm",
  "retrySafe": true,
  "statusUrl": "/api/v1/runs/inv-.../actions/op-..."
}
```

UI状态语义：

| operation state | UI文案/行为 |
|---|---|
| `reserved` | 正在提交；禁用重复按钮，保留同一key |
| `delivered` | 已送达执行系统，等待Workflow确认；不得显示业务已继续 |
| `delivery-unknown` | 送达结果未知；突出warning，可用同一key重试/查询，绝不生成新key自动重发 |
| `accepted-by-workflow` | Workflow已接受；刷新projection后才显示节点推进 |
| `rejected-by-workflow` | Workflow拒绝/过期；显示安全reason并允许按新projection决定下一步 |

浏览器refresh/crash后从server ledger恢复operation状态。DOM mutation、toast或HTTP 2xx本身都不能改Run业务状态。

## Loading、empty、error 与 stale state

每个页面必须有明确状态：

- initial loading使用与最终结构一致的skeleton并保留页面标题；
- empty Worker/Version/Run提供有权限的下一步，无权限时只解释empty；
- filter empty与Tenant empty区分；
- partial health允许展示已知org record，同时标出Kubernetes/Temporal/projection哪一来源不可用；
- publish pending显示Kubernetes ready、poller与probe逐项进度；失败显示可复制request ID，不暴露credential；
- stale `ETag`/projection返回412/409后保留用户输入，刷新server state并要求重新确认；
- action delivery-unknown使用persistent inline warning，不只toast；
- Run projection invalid时不渲染猜测图，展示contract violation与operator diagnostic入口（如有权限）；
- offline/timeout不把上次缓存状态标成实时，显示“last updated”。

## Accessibility 基线

- 目标为WCAG 2.2 AA；所有主流程可键盘完成。
- semantic heading、landmark、真实button/link/form label；table在移动视图保留header语义。
- modal具备focus trap、初始focus、Escape close、close后返回trigger；destructive action二次确认。
- Tenant switcher、tabs、combobox、DAG node与action使用正确ARIA状态；不伪造button div。
- status不只依赖颜色；focus ring可见；文本/图形对比达到目标。
- live region只播报关键publish/action/Run transition，避免每次poll重复朗读。
- DAG提供同等信息的结构化list视图，包括node status、dependencies、reason与actions。
- respect reduced motion，pan/zoom支持键盘与reset/fit操作。

## 三个 Samples 的 UI/API 验收

### Hello

1. SDK registration accepted后UI只读展示三个node templates和两个Activities；UI没有manifest upload/edit入口。
2. probe verified后version Ready/Current；trigger input按schema验证。
3. Run detail显示`prepare-greeting → compose-greeting → completed`，结果含实际WorkerVersion。
4. 显式历史版本触发显示selected历史version/Pinned，不错误标成Current。

### Parallel confirmation

5. Run启动只显示`approval-gate=waiting-for-user`、block reason与`confirm`action，Activity尚未执行。
6. schema form提交携带operation key与projection ETag；权限拒绝、invalid schema、stale state均不发Signal。
7. delivery-unknown不显示accepted；安全重试复用key。
8. accepted后动态出现BuildPlan、两个并行branch、join、finalize；renderer不依赖固定六节点HTML。
9. Worker restart前后同runtime node identity保持一致。

### Dynamic decision

10. concise Run显示concise completed、detailed skipped；detailed Run相反。
11. skipped node保留`route-not-selected`reason并参与finalize dependency展示。
12. 两条Run均由同一manifest catalog渲染实际runtime path；UI不从Event History或input自行猜route。

### Tenant与安全

13. 两Tenant同Worker/Run locator无数据串扰；伪造tenant字段被拒绝。
14. 无permission action/button服务端拒绝，即使直接调用API。
15. API JSON不暴露Task Queue、Worker Deployment、Kubernetes name、Temporal credential或raw Signal。
16. Routines不出现在nav、表单、trigger source或mock数据中。

## TDD 实施顺序（获批后）

1. HTTP error/auth/Tenant middleware contract tests；
2. read-only Worker/Version/manifest/probe JSON endpoints；
3. WorkerVersion publish与description revision endpoints；
4. Workflow trigger、Current/历史版本与idempotency endpoints；
5. Run read model、validated dynamic projection与cancel endpoints；
6. action Gateway HTTP adapter与delivery state tests；
7. server-rendered/app shell、routing与reference visual tokens；
8. Worker/Version/Workflow/Run pages；
9. dynamic DAG renderer与action form；
10. accessibility/responsive/error-state tests；
11. Hello → parallel-confirmation → dynamic-decision browser E2E。

每一步先写会失败的测试，再最小实现。不得先复制参考HTML的mock data/DOM action后再补真实接口。

## 2026-08-02 Console 与 local kind 紧急澄清

**Approved — 用户已授权按本节直接修复。** 本节只收紧既有 Console、publish 与共享 platform Kubernetes Namespace 契约，不引入 010 的未批准策略。

### New Worker 交互

- Console 禁止使用 `window.prompt`、`window.confirm` 等浏览器原生输入框创建 Worker。
- “创建 Worker”打开站内 `<dialog>`，并复用“录入版本”的同一 modal 组件与版式约定：`.dialog`、`.dialog-head`、`.form-grid`、`.form-actions`、关闭按钮和取消/提交按钮的语义与样式保持一致。表单必须包含显式 `Worker name` label、与服务端一致的格式提示和客户端基础校验、提交与取消操作、inline error 与 `aria-live` 状态。
- 打开时 focus 落在 name 输入；Escape、取消按钮和关闭按钮均不提交；关闭后 focus 返回触发按钮。服务端仍是最终校验者，失败时保留输入并在 dialog 内展示安全错误。

### WorkerVersion publish 最小公开输入

新的公开 publish request 仅接受：

```json
{
  "version": "2026.08.2",
  "description": "本版本做什么；创建时必填。",
  "image": "registry.example.com/worker@sha256:<64-lowercase-hex>",
  "runtime": {"cpu": "100m", "memory": "128Mi"},
  "versionConfig": {}
}
```

- `versionConfig`可省略并默认 `{}`；Console 将其放在明确的高级设置中，不作为首要发布步骤。`runtime`仍是用户可设置的资源边界。
- `source`、`repository`、`branch`、`commit`、`ciReference`以及 provenance aliases 不再是公开写入字段；strict decoder 必须拒绝客户端提交这些字段。审计主体、请求 ID、镜像观测身份等可信 metadata 由服务端产生。
- 既有 WorkerVersion 中已经保存的 source provenance 继续可读，以保持数据兼容；新记录允许该旧字段为空。UI 不再要求、展示或编辑 provenance。
- contract、manifest、projection 与其 digest 仍只来自 Org SDK bootstrap registration，不能随 publish request 上传。

### 共享 platform Kubernetes Namespace reconcile

- 在 apply 任一从属于 platform Kubernetes Namespace 的 Worker 资源前，Kubernetes adapter 必须确保配置的单一共享 platform Kubernetes Namespace 存在。
- adapter 先执行 read：platform Kubernetes Namespace 已存在即直接使用，不修改 labels、annotations、owner 或其他内容，也不删除其中任何资源。
- 仅在 Kubernetes 明确返回 NotFound 时，org 才创建该 platform Kubernetes Namespace，并给新建对象加 `app.kubernetes.io/managed-by: org`。并发创建的 AlreadyExists race 通过重新读取收敛；认证、连接等非 NotFound 错误不得降级为 create。
- Worker workload apply payload 不再包含 platform Kubernetes Namespace resource object，避免 server-side apply 接管一个预先存在的共享 platform Kubernetes Namespace。
- reconcile 失败必须使 publish operation 进入可轮询的 `failed` 状态并返回安全错误；不得遗留 `running`、泄露 bootstrap credential，或把失败版本标为 Ready/Current。
- 失败的 WorkerVersion identity 仍是不可变记录。修复环境后，用户以新 version 发布；Console 不暗中覆盖旧失败版本。
- publish conflict 不得统一显示为模糊的“resource state conflict”：同名不可变 version 已存在时提示用户改用新 version；同一 `Idempotency-Key` 配不同 canonical payload 时提示复用原 payload 或换 key；同 key、同 payload 的 `running` operation 必须返回并继续轮询原 operation，而不是创建第二次发布。

### local kind Console registry 默认值

- `make console-dev`是本仓库 local kind walkthrough 的维护者入口；未显式设置`ORG_REGISTRY_ALLOWLIST`时，它必须仅为该进程默认使用`org.local,ghcr.io`，使`make kind-load`输出的`org.local/...@sha256:...`可发布。
- 调用者显式提供`ORG_REGISTRY_ALLOWLIST`时必须完整覆盖该默认值；不得把`org.local`隐式加入production binary的通用配置默认值。
- Getting Started继续显示可复制的显式环境命令，同时说明直接`make console-dev`采用上述local-dev默认值。registry校验仍只接受完整immutable digest，不因此接受tag。

## 待确认的实现取舍

四项产品契约已由用户确认；开始实现前仍建议确认以下实现层取舍：

1. **前端交付形态（推荐）**：MVP使用Go server-rendered HTML +少量progressive enhancement JavaScript，保持单一binary和清晰HTTP契约；dynamic DAG renderer作为独立client component。若选择SPA，需要另定toolchain/build/dependency策略。
2. **contract来源（已批准）**：UI不录入artifact。Org SDK startup从typed Definition构造并bootstrap registration；server保存后只读展示，并以post-poller pinned probe核验。
3. **publish响应（推荐）**：采用`202 Accepted` + operation polling，避免HTTP请求阻塞Kubernetes readiness与Temporal poller/probe；现有同步application method可先由后台job adapter包装。
4. **Run更新（推荐）**：MVP使用ETag条件polling；SSE/WebSocket等实时push在实际规模证明需要后另加。
5. **DAG layout（推荐）**：采用依赖驱动的layered layout +移动端结构化list；具体layout library在实现slice选择并锁版本，不把固定坐标写入contract。
