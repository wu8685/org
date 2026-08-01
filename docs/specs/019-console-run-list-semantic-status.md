# Console Run list semantic status

> Terminology follows the canonical [org glossary](../architecture/glossary.md). Product isolation is always a Tenant; this feature does not expose either platform Namespace.

## 状态

**Approved — implementation authorized by the user on 2026-08-02.**

本规格是 [`011-console-ui-http-api.md`](011-console-ui-http-api.md) 的独立 Runs-list amendment。它不改变 Run detail、Trigger editor、Sample demo latency 或 Temporal execution contract。

## 用户结果

Runs tab 无需打开详情即可看到每个 Run 的最新业务状态：`running`、`waiting-for-user`、`completed`、`failed`、`cancelled`。列表同时显示安全的当前节点摘要、更新时间；等待用户操作时显示明确但不含输入数据的 block reason。

所有状态来自 control plane 已有的 durable Invocation 与经过 contract validation 的 Org SDK semantic projection。Console 不读取或推断 Temporal Event History。

## Read model

`GET /api/v1/runs` 保留现有字段，并为每项增加：

- `semanticStatus`：稳定产品状态。存在 waiting node 时把 projection 的 `running` 提升为 `waiting-for-user`；durable cancelled/failed/completed Run state 优先表示相应终态；
- `projectionRevision`：成功读取的 semantic projection revision；
- `currentNodes`：仅含当前节点的用户可见 label 与 status，不含 payload、result、Signal input 或平台 routing ID；
- `currentNodeSummary`：供紧凑列表显示的安全 label 摘要；
- `blockReason`：只使用 org 固定文案，例如 `Waiting for an authorized user action`。不得直接回传用户代码提供的 arbitrary reason、Activity error、action payload 或 Secret；
- `semanticUpdatedAt`：durable Invocation 更新时间。当前 query-only projection 的 replay timestamp 不作为 collection freshness source；相同 revision 下即使 adapter 返回漂移 timestamp，也不能使 ETag 抖动。语义变化由 projection revision 与安全摘要驱动，终态由 durable Invocation 更新时间标记。

projection 暂不可用时保留 durable Invocation 状态，并返回 `projectionStatus: unavailable`；不得从 execution history 猜测业务节点。

## Conditional refresh

Runs collection 响应提供绑定当前 Tenant ID 与完整安全 read model 的 ETag，并设置 private/no-cache。相同 Tenant、相同 read model 的 `If-None-Match` 返回 `304`；不同 Tenant 即使资源字段恰好相同也不能共享 ETag。

Runs 页面在可见时低频轮询，复用 collection ETag。`304` 保持现有 DOM；新 revision 更新状态、节点与时间。切换 Tenant 后整页重定向，不能携带旧 Tenant 的内存列表。初次加载显示 skeleton，空结果显示当前 Tenant empty state，poll failure 保留已有列表并使用现有 notice/ARIA error feedback。

## UI 与可访问性

- 状态 badge 必须包含可读文本，颜色只作辅助；`canceled` API 状态在 UI 显示为 `Cancelled`；
- waiting block reason 与当前节点摘要均由 `textContent` 构造，server-rendered shell 与 progressive JavaScript 不使用不可信 HTML；
- table desktop 与现有 `<=700px` structured mobile rows 使用相同 Status、Current node、Updated data labels；
- waiting 文案具备可读的 ARIA label，且不展示 action input schema value、payload 或 Secret。

## TDD 与验收

1. API tests 覆盖 running、waiting-for-user、completed、failed、canceled，waiting 安全文案、current node、更新时间与 JSON HTML escaping。
2. collection ETag/304 tests 覆盖 projection revision 变化，以及两个 Tenant 的 ETag 不同。
3. Tenant switch tests 证明 Run list/GetInvocation 只使用当前 authenticated Tenant；同名 Workflow/Run metadata 不串数据。
4. UI contract tests 覆盖文本状态、ARIA、mobile data labels、poll/visibility/ETag、empty/loading/failure。
5. 真实双 Tenant HTTP/browser smoke 验证列表直接显示状态，切换后数据刷新且不泄露另一 Tenant。
6. root race/vet/docs tests 通过；独立 commit/push，不混入其他 slice。
