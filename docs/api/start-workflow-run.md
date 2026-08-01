# 启动 Workflow Run

一次触发创建一个独立 Run。用户选择 Worker 下的 Workflow，可选指定一个 Ready 历史 Version；未指定时使用 Current。

## Console 输入

Trigger 对话框只有一个结构化 payload editor：

- 默认使用 YAML，也可切换为 JSON；
- 支持嵌套 object、array、scalar 和空值；
- input schema 只读展示，可把 schema-derived example 复制到 editor；
- 不根据 schema 生成 `name`、`subject` 等固定表单字段；
- YAML 自定义 tag、anchor、alias、merge key 和环境变量展开均被禁用；
- 解析失败会保留原文，并显示 path 与 YAML line；schema validation 失败会显示服务端返回的 `$.field` path。

Run description 是可选的 plain text，用于说明“为何启动这一次 Run”。它最多 1000 个 Unicode code points、20 行，不属于 Workflow payload、contract 或 schema。不要在 description 或 payload 中填写 Secret。

## HTTP/JSON 请求

HTTP API 始终接收 JSON；YAML 只存在于 Console 展示和编辑层。

```http
POST /api/v1/workers/{workerName}/workflows/{workflowName}/runs
Content-Type: application/json
X-CSRF-Token: <session.csrfToken>
Idempotency-Key: <unique-run-start-key>
```

```json
{
  "workerVersion": "2026.08.1",
  "description": "验证本地 Hello 发布",
  "input": {
    "name": "Codex"
  }
}
```

`workerVersion` 和 `description` 均可省略。空 editor 会作为 JSON `null` 提交，是否允许由该 Workflow 的 schema 决定。

先在同一认证 session 中调用 `GET /api/v1/session`，从响应取得 CSRF token。route 中的 Worker 与 Workflow、当前 Tenant 和 principal 均由服务端认证与授权；请求 body 不接受 `tenantId`、`scope`、Task Queue 或底层 credential。

## Idempotency

同一 Tenant、principal 作用域内：

- 相同 `Idempotency-Key`、canonical payload、description 和显式 Version 返回同一个 Run；
- 复用 key，但改变其中任一项，返回 `409 run_idempotency_conflict`；
- 不提供 key 时，每次调用都创建独立 Run。

Run list、detail 和 Tenant-scoped Audit 会显示规范化后的 description。description 不会传给 Temporal Workflow，也不会改变 SDK contract digest。
