# Parallel Confirmation Org SDK Worker

> 术语遵循 [org glossary](../../docs/architecture/glossary.md)：产品隔离边界称 Tenant；底层资源称 shared platform Temporal Namespace 与 shared platform Kubernetes Namespace。

这个 Sample 演示一个先等待人工确认、再动态并行、最后汇合的 Workflow：

```text
approval-gate → build-plan → execute summary ─┐
                              execute readiness ─┴→ join → finalize
```

`approval-gate` 是 Workflow 内的 idle node，不是阻塞 Activity。确认前不会调度任何 Activity；确认后，`BuildPlan` 的 recorded result 产生两个稳定 branch key，Workflow 在等待任一结果之前先启动两个分支。

## 阅读路径

- [`definition.go`](definition.go)：typed Definition、`AwaitConfirmation`、runtime fork/join。
- [`activities.go`](activities.go)：无外部副作用的 plan、branch 与 finalize 逻辑。
- [`generated/org-worker-manifest.json`](generated/org-worker-manifest.json)：由 Definition 生成的只读 contract，不应手工编辑。

Sample 不 import raw Temporal SDK，不手写 Signal、projection 或 metadata。Org SDK 负责 stable runtime node/Activity ID、Pinned Workflow registration、action envelope、dynamic projection 与 canonical manifest。

```sh
cd samples/parallel-confirmation
go test ./...
go run ./cmd/generate-manifest
```

## Projection 与受控确认

启动后 projection 只包含 `approval-gate = waiting-for-user`，并公开 schema 化的 `confirm` action。org Gateway 校验 Tenant、`run:action:confirm` permission、input schema 与 `Idempotency-Key` 后发送受控 action；不要让终端用户直连 Temporal，也不要直接发送 Signal。

确认后 projection 按实际路径增加 `build-plan`、两个 `execute-branch` runtime node、`join` 和 `finalize`。renderer 必须消费 projection 中的动态节点和 dependencies，不能假定固定节点数或坐标。

## 构建并加载 kind

在仓库根目录运行：

```sh
make parallel-sample-kind-load \
  SAMPLE_VERSION=2026.08.1 \
  SAMPLE_COMMIT=$(git rev-parse --short=12 HEAD)
```

命令从仓库根构建 Sample image，并输出 immutable `IMAGE_DIGEST`。org 只接收 image digest、generated manifest、版本级 description、runtime resources 和 source provenance；控制面不为用户构建或发布镜像。

kind Pod 的 `TEMPORAL_ADDRESS` 必须使用 Pod 可达地址；本地通常是 `host.docker.internal:7233`，不能硬编码 `127.0.0.1`。`TEMPORAL_TASK_QUEUE` 与 `TEMPORAL_WORKER_DEPLOYMENT` 由服务端从 Tenant + Worker name 派生。

所有 Activity 都声明 `sideEffect: none`。真实 write Activity 必须声明 stable idempotency key，或 reconciliation/compensation policy；平台不声称外部效果 exactly once。
