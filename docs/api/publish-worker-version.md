# 发布 WorkerVersion

本页说明如何把已经构建完成的 Worker image 发布成 org Version。第一次体验建议使用 Console；需要 CI 或自动化集成时，再使用这里的 HTTP API。

## 发布前需要准备什么

- 已存在的逻辑 Worker，例如 `hello-worker`。
- registry 或 Sample `make kind-load` 返回的 immutable `IMAGE_DIGEST`。
- Version label 和 description。
- Pod CPU/memory、Secret reference 等 runtime 配置。
- repository、branch、commit 和 CI reference 等 source provenance。

org does not build or push image。它从 immutable `registry/repository@sha256:<64 lowercase hex>` 开始接手。

## 发布后会发生什么

```text
submit publish request
  → candidate deployment
  → SDK automatic registration
  → Worker polling
  → pinned contract probe
  → Ready / Current
```

API 返回 operation，调用方轮询 operation 状态。Version 在全部检查完成前保持 pending。

## 第 1 步：读取 session 和 CSRF token

保持同一个 authenticated session（相同 cookie 或 Authorization），读取 session：

```http
GET /api/v1/session
Accept: application/json
```

从响应的 `session.csrfToken` 取得 CSRF token。

## 第 2 步：提交发布请求

使用同一个 authenticated session：

```http
POST /api/v1/workers/{workerName}/versions
X-CSRF-Token: <session.csrfToken>
Idempotency-Key: publish-2026-08-1
Content-Type: application/json
```

请求体结构见 [`examples/publish-worker-version.json`](examples/publish-worker-version.json)。将其中的 image 替换为准确 `IMAGE_DIGEST`。

| 字段 | 含义 | 不应包含什么 |
|---|---|---|
| `version` | 稳定的公开 Version label | 可变部署状态 |
| `description` | 供人阅读的版本说明 | Secret 或 credential |
| `image` | immutable OCI digest | tag、`tag@digest`、源码地址 |
| `versionConfig` | Version 的业务配置 | 平台 routing |
| `runtime` | Pod CPU/memory 和 Secret reference | Secret value |
| `source` | repository、branch、commit、CI provenance | 用于运行时认证的 credential |

Worker name 来自 URL，Tenant 来自认证主体。请求体不得自行发送 Tenant identity、Worker name、`scope`、平台 routing、credential、contract、metadata 或 projection 字段。

## Idempotency-Key

`Idempotency-Key` 必须包含 1–200 个 visible ASCII 字符（`!`–`~`，不含空格）。

- 相同 Tenant、principal、key 和 canonical payload：返回同一 operation。
- 同一 scope 下复用 key，但改变 payload：返回 `409 conflict`。
- JSON object 字段顺序和无意义空白：不改变 canonical payload。
- terminal reservation：默认保留 24 小时。

不要把 credential 或敏感业务值放进 key。

## 第 3 步：检查响应

API 返回 `202 Accepted` 和可轮询的 operation URL。成功条件不是“HTTP request 已接受”，而是发布流水线最终进入 Ready / Current。

注册后的 contract 在 Console/API 中只读。如果 image 或 contract 无效，应修复后发布新的 Version，不要覆盖已有 Version。

## 常见失败

| 失败 | 含义 | 处理方式 |
|---|---|---|
| image reference 被拒绝 | 不是允许 registry 下的完整 digest，或使用了 tag | 使用 push/`kind-load` 返回的准确 `IMAGE_DIGEST` |
| registration rejected | 运行中的 SDK contract、image、protocol 或 workload identity 不满足发布约束 | 修复 Worker 后发布新 Version |
| poller/probe 未通过 | candidate 已启动，但尚不能证明目标 Workflow 可由该 Version 正确服务 | 查看 operation、candidate Pod 和高级诊断信息 |
| idempotency conflict | 同一 key 被用于不同 payload | 为新的发布意图使用新 key |

## Secret 与外部副作用

`runtime.environment` 包含 Secret reference，不包含 Secret value。不要把 credential 或敏感 input 放入 Version configuration、Workflow history、projection、log 或 Audit。

Workflow 代码不得执行外部 I/O。write Activity 必须传播稳定的 idempotency key，或声明 reconciliation/compensation 行为；retry 不保证外部效果 exactly once。

> 产品术语遵循 [org glossary](../architecture/glossary.md)。Tenant 来自 authenticated principal，发布请求不能指定另一个 Tenant。
