# Org SDK Samples

三个 Sample 展示从最小顺序 Workflow 到人工确认、动态并行和运行时路由的渐进路径。

| Sample | 适合先看什么 | 运行结果 |
|---|---|---|
| [Hello](hello/README.md) | Definition、Activity、顺序 DAG | 两个业务步骤后返回问候语 |
| [Parallel confirmation](parallel-confirmation/README.md) | `AwaitConfirmation`、fork/join | 用户确认后并行执行两个分支并汇合 |
| [Dynamic decision](dynamic-decision/README.md) | Activity result 驱动 if/else | 只执行一个分支，另一分支显示 `skipped` |

## 最小使用路径

1. 修改 Sample 的 `definition.go` 与 `activities.go`，表达业务步骤、依赖、输入输出和 action。
2. 运行对应 `make *-sample-test` 目标。
3. 构建并推送自己的 OCI image；发布时只提交 registry 返回的不可变 `image@sha256:...`，不要提交 tag。
4. 在 Console 创建 Worker，并以版本、description、digest、runtime config 与 source provenance 发布 WorkerVersion。
5. 候选 Pod 启动后，Org SDK 自动从 typed Definition 构造 contract 并注册；不需要镜像内 manifest 文件，也不需要在 Console 上传或编辑 manifest。
6. 从 Console 触发 Workflow，观察动态 DAG。人工 action 通过 Gateway 提交；路由 Sample 会显示 selected 与 `skipped` 节点。

本地完整验证需要 `kind-org` 与 `127.0.0.1:7233` 的 Temporal：

```sh
make e2e-local
make parallel-e2e-local
make dynamic-e2e-local
```

不要把 Secret 写进 image、Workflow input、projection、log 或 Audit。version config 只保存 Secret reference。Workflow 不能直接执行外部 I/O；write Activity 必须使用稳定 idempotency key，或明确 reconciliation/compensation policy。
