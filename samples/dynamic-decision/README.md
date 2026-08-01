# Dynamic Decision Org SDK Worker

> 术语遵循 [org glossary](../../docs/architecture/glossary.md)：产品隔离边界称 Tenant；底层资源称 shared platform Temporal Namespace 与 shared platform Kubernetes Namespace。

这个 Sample 最小化演示由 recorded Activity result 驱动的 runtime if/else：

```text
determine-route ─┬→ concise-branch  ─┐
                 └→ detailed-branch ─┴→ finalize
```

输入 `mode: concise` 时只执行 concise Activity，detailed candidate 仍在 projection 中且状态为 `skipped`；`mode: detailed` 时相反。`finalize` 同时依赖 completed selected node 与 skipped candidate。

## 为什么 route 来自 Activity result

Workflow 不能直接访问外部服务。`DetermineRoute` 是 Activity，它的 result 由 Temporal 记录；Workflow 只基于这个 recorded result 做 deterministic `switch`，再通过 Org SDK 创建实际节点、标记未选节点并执行共同 finalize。Sample 本身没有外部系统，重点是决策语义。

- [`definition.go`](definition.go)：typed Definition、recorded route、selected/skip/finalize。
- [`activities.go`](activities.go)：四个无外部副作用的业务 Activity。

Sample 不 import raw Temporal SDK，不手写 projection、Signal 或 metadata。UI/调用方只消费 SDK dynamic projection，不从 Temporal Event History 推断路径。

```sh
cd samples/dynamic-decision
go test ./...
```

## 两条运行结果

```json
{"mode":"concise","subject":"release notes"}
```

projection 中 `concise-branch=completed`、`detailed-branch=skipped`，reason code 为 `route-not-selected`。

```json
{"mode":"detailed","subject":"release notes"}
```

projection 中 `detailed-branch=completed`、`concise-branch=skipped`。不支持的 mode 产生稳定 failure，不自动选择 fallback。

## 构建并加载 kind

在仓库根目录运行：

```sh
make dynamic-sample-kind-load \
  SAMPLE_VERSION=2026.08.1 \
  SAMPLE_COMMIT=$(git rev-parse --short=12 HEAD)
```

命令从仓库根构建 Sample image，加载到 `kind-org` 并输出 immutable `IMAGE_DIGEST`。org 控制面只接收 image digest、版本级 description、runtime config 与 source provenance，不代建或发布用户镜像。Org SDK 在候选 Pod 启动时自动注册只读 contract；不需要 image 内 manifest 或 Console 上传。

生产环境由用户 CI build/push，并从 registry push 结果保存 `image@sha256:...`。在 Console 创建 `dynamic-decision-worker` 的 WorkerVersion 时提交该 digest；不要提交 `latest` 或版本 tag。

kind Pod 的 `TEMPORAL_ADDRESS` 使用 Pod 可达的 endpoint（本地通常为 `host.docker.internal:7233`），不能硬编码 `127.0.0.1`。Task Queue、Worker Deployment、Workflow ID 和 Kubernetes workload name 由服务端根据 Tenant + Worker name 派生。

四个 Activity 均声明 `sideEffect: none`。真实 write Activity 必须使用 stable idempotency key或reconciliation/compensation policy；平台不声称外部效果 exactly once。
