# Safe Run failure information

> Terminology follows the canonical [org glossary](../../user/architecture/glossary.md). This amendment exposes product-safe Workflow failure information; it never exposes platform Namespace, Temporal history, credentials or raw Worker errors.

## 状态

**Approved — implementation authorized by the user on 2026-08-02.**

本规格在 [`019-console-run-list-semantic-status.md`](019-console-run-list-semantic-status.md) 完成后独立实施，是 [`006-org-sdk.md`](006-org-sdk.md) 与 [`011-console-ui-http-api.md`](011-console-ui-http-api.md) 的 failure-contract amendment。Tenant management 另行实施，不混入本 slice。

## 用户结果

失败 Run 不再只显示 `Failed`。Run list 显示简短、安全的 error summary；Run detail 显示 stable code、用户可读 message、失败 semantic node/Activity 与 occurred time。dynamic-decision 收到不支持的 mode 时返回：

```yaml
code: invalid_route
message: Unsupported mode. Choose concise or detailed.
node: Determine route
```

message 不回显非法 mode。Console 不展示 raw Activity error、panic、stack、Temporal failure/history、外部响应或 action/input payload。

## Org SDK contract

Org SDK 提供 typed safe user error constructor。用户 Activity 可返回 stable code 与用户消息；SDK Activity adapter 将其编码为 reserved non-retryable application failure，Workflow adapter 只提取该 reserved type，并写入 semantic projection：

```json
{
  "failure": {
    "code": "invalid_route",
    "message": "Unsupported mode. Choose concise or detailed.",
    "runtimeNodeId": "...",
    "templateId": "determine-route",
    "nodeLabel": "Determine route",
    "occurredAt": "..."
  }
}
```

- code 必须匹配 `^[a-z][a-z0-9_]{0,63}$`；message trim 后为 1–300 Unicode code points、最多 4 行且不得含其他 control character；
- runtime node/template/label 由 SDK graph 注入，用户 error 不能伪造；occurredAt 使用 deterministic Workflow time；
- untyped error、panic、malformed/oversize safe error 统一映射为 `activity_failed` / `Activity failed. Open advanced diagnostics if authorized.`；Workflow 层无节点错误映射为 `workflow_failed`；
- projection 仍保留失败 node status，但不得把 raw error 放进 `ReasonCode` 或 RecentEvents。

Workflow determinism 不变；外部 I/O 仍只能在 Activity。safe failure 是展示契约，不替代 retry、idempotency、reconciliation 或 advanced diagnostics。

## Control plane 与 durable read model

control plane 重新验证/归一化 projection failure，不信任 Worker 文本。invalid code、oversize、control character、未知 node identity 或非 failed Run 一律丢弃并生成 bounded generic fallback。

安全 failure 写入 Tenant-scoped Invocation durable state，字段为 code/message/runtime node/template/label/occurredAt。首次成功读取 failed projection 后持久化；进程重启和 subsequent query failure 仍可返回同一 safe failure。成功与 cancelled Run 不保存/返回 failure，避免把 cancel 误标为 failed。

`GET /api/v1/runs` 每项增加 bounded `errorSummary`；`GET /api/v1/runs/{id}` 增加完整 `failure`。cross-Tenant locator 继续 `404`。HTTP canonical JSON、Trigger input 与 SDK manifest digest contract 不变。

## Console

- failed list badge 下显示最多 160 Unicode code points 的安全 message；完整 message 仍受 300 code-point server bound；
- Run detail 在 Live status 前后显著显示 `role="alert"` failure panel，包括 code/message/node/time；
- DAG failed node 继续使用文本 status，failure panel 与 node label 对应；
- 所有内容通过 `textContent`/server HTML escaping 构造，禁止 `innerHTML`；无 failure 的成功/cancelled Run 不渲染 error panel；
- polling/ETag 包含 durable safe failure；delivery/action payload 不进入 failure。

## TDD 与交付

1. SDK red tests：typed invalid-route extraction、raw error/panic generic mapping、code/message bounds、successful Workflow has no failure。
2. service/FileStore red tests：projection revalidation、durable restart、cancelled/success no failure、Tenant isolation。
3. API/UI red tests：list summary、detail full failure、XSS escaping、oversize redaction、failed node、ARIA panel、ETag。
4. dynamic-decision Sample first returns typed `invalid_route` without echoing input；README explains supported values and safe failure.
5. real dynamic kind+Temporal E2E triggers invalid mode, verifies failed projection, list/detail API and Console browser panel, then cleans up.
6. 因独立 Sample 使用 versioned public Org SDK，先提交/push verified SDK/control-plane/Console phase，再将 Sample module 升级到该 commit，完成 real E2E 后提交/push Sample phase。
7. 每阶段 root/Sample race、vet、docs checks 通过；不混入 Tenant management 或 010 Draft。
