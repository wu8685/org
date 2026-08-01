# 后续 UI/API SDD 设计输入

> 本文遵循 [org glossary](architecture/glossary.md)。产品隔离边界称 Tenant；底层资源只称 one shared platform Temporal Namespace 与 one shared platform Kubernetes Namespace。

## 状态与实施门槛

**Superseded design input — 已转化为 `specs/011-console-ui-http-api.md` Draft；两份文档都不授权 UI 或 HTTP handler 实现。**

Org SDK 与 Hello、parallel-confirmation、dynamic-decision 三个 Samples 已按顺序完成验证。正式产品/API/验收契约现由 `specs/011-console-ui-http-api.md` Draft承载；用户确认011前不得实现UI/API。

## 参考原型

用户提供 `org-worker-console.html` 作为视觉与信息架构参考；当前本机文件 SHA-256 为：

```text
d34faa7e217f1e9b9396886fdcc8de5d4b3217bd01459214aecd7a7ffdbde0c7
```

接受的方向：

- Tenant → Worker → Version → Workflow → Run 的信息架构；
- 简洁、留白充分的控制台风格；
- Tenant switcher、Worker/version detail、Workflow trigger、Runs 与 Run detail 的主要导航关系；
- Temporal Web 仅作为高级诊断 deep link。

原型是设计参考，不是产品行为的权威来源。以下四项必须在正式 SDD 中修正。

## 必须写入正式 SDD 的产品契约

### 1. WorkerVersion 录入与只读 SDK contract

- WorkerVersion 录入 JSON 可以承载版本配置和 runtime config。
- SDK 生成的 manifest、workflow/node/action contract、schema、projection protocol 与 manifest digest 是只读 artifact contract；用户不能在 UI 手填或改写。
- UI 只能展示注册值及 contract probe 的验证结果，并清楚区分“submitted”“probe verified”“mismatch/unsupported”。
- version-level description 仍按 004 的 revision / If-Match 规则独立更新，不能改变 image、runtime、manifest 或 Temporal Build ID。

### 2. Dynamic projection 驱动 DAG renderer

- Workflow/Run DAG renderer 只消费 Org SDK dynamic semantic projection，不解析 Temporal Event History。
- renderer 必须支持运行前未知节点数、if/else 的 `skipped`、data-driven fan-out、join、并行 current nodes、waiting/failed/timed-out/canceled 状态和节点级 allowed actions。
- 不得沿用原型中固定六节点、固定绝对坐标或浏览器本地推导 DAG 的实现方式。
- layout 是 projection 的视图；node/edge/status identity 来自服务端验证后的 projection。

### 3. 人工 action 必须经过 Gateway

- action 表单由 manifest 声明的 JSON Schema 渲染，并展示 required permission、waiting/block reason 与目标 runtime node。
- 提交必须调用未来 Gateway action API，携带稳定 `Idempotency-Key` / operation ID；Gateway 从认证主体派生 Tenant，执行授权、schema 校验、stale projection 校验、审计与去重后发送 reserved Signal。
- UI 必须区分 `delivered`、`delivery-unknown`、`accepted-by-workflow` 与 `rejected-by-workflow`；transport success 不能显示为业务已接受。
- 浏览器不得仅修改 DOM 来假装 Workflow 已推进，也不得接触 Temporal 直连凭证或 raw Signal 名称。

### 4. Routines 暂不属于 MVP

- 当前领域、API 与 Org SDK 没有 cron 或事件触发的 Routine 能力。
- Routines 必须另写独立 SDD，覆盖 schedule/event source、Tenant authorization、version selection、idempotency、delivery、audit、quota 与 failure semantics。
- 在该 SDD 获批并实现前，MVP UI 隐藏 Routines；若为了信息架构预览而保留入口，必须明确标为“未提供”，不可创建、启用或展示虚构运行结果。

## 正式 SDD 的最低输出

后续正式 UI/API SDD 至少给出：页面/路由矩阵、服务 application contract、认证与权限、动态 projection renderer 输入、action request/response 状态机、manifest/probe read model、error/loading/empty states、responsive/accessibility 基线，以及与三个 Samples 的验收场景。原型中的纯前端 demo 数据和 DOM action 不作为验收依据。
