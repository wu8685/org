# Hello Org SDK Worker

这是最小的 Org SDK Worker repository。它用两个无外部副作用的 Activity 生成问候语，并把顺序执行路径投影给 org：

```text
prepare-greeting → compose-greeting → completed
```

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 这个 Sample 教什么

完成后，你应该能说明：

- Definition 如何声明 Workflow 和 Activity。
- 两个 Activity 如何按依赖顺序执行。
- Org SDK 如何把执行过程投影成三个业务节点。
- 一个独立 Worker repository 如何测试、构建并输出 immutable digest。

## 先看两处

- `definition.go`：typed Definition、节点依赖与 retry/timeout policy；
- `activities.go`：业务输入校验和问候语生成。

Sample 不 import raw Temporal SDK，不手写 projection 或平台 routing。Org SDK 负责 stable node/Activity ID、dynamic semantic projection 和启动时 contract registration。

为了让第一次演示时能看见处理中状态，`ComposeGreeting` Activity 默认包含约 10 秒的教学演示延迟。它只发生在 Activity 中，可通过 `WithComposeGreetingDelay` 调整；Workflow 本身不 sleep，也不影响 replay determinism。

## 测试

从本目录运行：

```sh
make test
make vet
# 或一次执行
make verify
```

测试注入 no-op sleeper，因此不会等待 10 秒。预期结果：输入 `{"name":"Codex"}` 后返回 `Hello, Codex!`，projection 中的三个节点均为 `completed`。

## 构建本地 image

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make image \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

Docker build context 只有当前 repository。命令输出可读 tag；发布 WorkerVersion 仍必须使用 immutable digest。

## Push 到 registry

先用 Docker 完成 registry login，再运行：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make push \
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

成功后输出：

```text
IMAGE_DIGEST=registry.example.com/team/hello-worker@sha256:<digest>
```

脚本不保存 registry credential。不要从 tag 文本推测 digest，始终使用 registry 返回值。

## 加载到本地 kind

已有 `kind-org` 时：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

它会构建、加载 image 并输出 `IMAGE_DIGEST=org.local/hello-worker@sha256:...`。

## 在 org 中运行

1. 在 Console 创建 Worker `hello-worker`。
2. 新建 Version，填写 version-level description、`IMAGE_DIGEST`、`100m` CPU 和 `128Mi` memory；无需填写源码来源或上传合同文件。
3. 等待候选 Worker 完成 SDK registration、poller 与 probe。
4. 触发 `HelloWorkflow`，input 为 `{"name":"Codex"}`。
5. 立即打开 Run detail：`prepare-greeting` 很快完成，`compose-greeting` 会保持约 10 秒 `running`，随后进入 `completed`。
6. 等待最终结果 `Hello, Codex!`。

Org SDK 在 Worker 启动时从 typed Definition 生成 contract 并自动注册。用户不管理 contract artifact，Console 只读展示注册结果。

> 这段延迟只为了让 Console 演示更容易观察，不代表真实业务处理。production Worker 不应照搬人为 sleep；删除该 option，让 projection 反映 Activity 的实际生命周期。

## 发布输入与平台配置

部署时 org 平台注入执行连接、候选 Pod identity 和一次性注册材料。它们不是用户填写的 `.env` 配置，也不得打进 image 或提交到 Git。

用户只维护业务 Definition/Activities 和 image。Version description、resources 与 Secret reference 通过 org 发布接口提交；可信审计 metadata 由平台记录。完整字段说明见 [发布 WorkerVersion](https://github.com/wu8685/org/blob/main/docs/api/publish-worker-version.md)。

真实 write Activity 必须使用 stable idempotency key，或声明 reconciliation/compensation policy；平台不声称外部效果 exactly once。
