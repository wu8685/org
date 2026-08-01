# Hello Org SDK Worker

> 术语遵循 [org glossary](../../docs/architecture/glossary.md)：产品隔离边界称 Tenant；底层资源称 shared platform Temporal Namespace 与 shared platform Kubernetes Namespace。

这是最小的 Org SDK Worker。它只演示一条顺序执行路径：

```text
prepare-greeting → compose-greeting → completed
```

输入 `{"name":"Codex"}`，得到：

```json
{"message":"Hello, Codex!","workerVersion":"v1","idempotencyKey":"<sha256>"}
```

## 1. 只需阅读两处

- [`definition.go`](definition.go)：用 typed Org SDK Definition 连接两个 Activity 和 terminal semantic node。
- [`activities.go`](activities.go)：纯业务输入校验与问候语生成。

Sample 不 import raw Temporal SDK，不注册 Query/Signal，也不手写 projection。Org SDK 负责 stable node/Activity ID、retry options、Pinned Workflow registration、dynamic semantic projection 与 canonical manifest。

```sh
make sample-test
```

两个 Activity 都声明为 `sideEffect: none`。真实 write Activity 必须改为 `sideEffect: write`，并通过 Org SDK 声明和传播 stable idempotency key，或声明 reconciliation policy；这不等于外部效果 exactly once。

## 2. SDK 自动注册 contract

Worker 启动时调用 Org SDK hosted entrypoint。SDK 从 typed Definition 在内存中生成 canonical contract/digest，使用平台注入的短期 credential 注册；收到 `accepted` 后才开始 Temporal polling。

```sh
make sample-test
```

不需要把 manifest 放进 image，也不需要在 API/Console 上传。`cmd/generate-manifest` 仅保留为可选的 CI/debug 审计工具，不是发布输入。

## 3. 构建并加载到 kind

在仓库根目录运行：

```sh
make sample-kind-load \
  SAMPLE_VERSION=2026.08.1 \
  SAMPLE_COMMIT=$(git rev-parse --short=12 HEAD)
```

脚本使用仓库根作为 Docker build context，以便 Sample 引用本仓库 Org SDK；它只构建用户 Sample image，不由 org 控制面代建或发布。成功后输出：

```text
IMAGE_TAG=org.local/hello-worker:2026.08.1-<commit>
IMAGE_DIGEST=org.local/hello-worker@sha256:<digest>
```

注册时必须使用 `IMAGE_DIGEST`，不能使用可变 tag。本地流程不 push registry。

生产发布由用户自己的 CI build/push。让 CI 保存 registry 返回的 digest，并组装 `registry.example.com/team/hello@sha256:...`；Console 只接受这个不可变 reference。不要从本地 tag 文本推测 digest。

## 4. 由 org 注册、启动和读取 projection

WorkerVersion 注册请求包含：

- `workerName: hello-worker`；
- 版本级 `description`；
- OCI image digest；
- version、runtime resources 与 source provenance。

触发语义是 `hello-worker` 下启动 `HelloWorkflow`；未指定版本时使用 Current，显式历史版本使用 Pinned override。读取 Run 时使用 SDK 的 semantic projection；不要从 Temporal Event History 推断 DAG。

kind Pod 的技术连接参数由 org 注入：

| Variable | Purpose |
|---|---|
| `TEMPORAL_ADDRESS` | Pod 可达的 platform Temporal endpoint；本地通常为 `host.docker.internal:7233` |
| `TEMPORAL_NAMESPACE` | shared platform Temporal Namespace |
| `TEMPORAL_TASK_QUEUE` | 服务端由 Tenant + Worker name 派生 |
| `TEMPORAL_WORKER_DEPLOYMENT` | 服务端由 Tenant + Worker name 派生 |
| `TEMPORAL_WORKER_BUILD_ID` | 本次 WorkerVersion |

Temporal Web UI 仅用于高级诊断。普通用户不需要直连 Temporal，也不接触 Task Queue、Signal 或 platform credentials。
