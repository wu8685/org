# Parallel Confirmation Org SDK Worker

这个独立 Worker repository 演示“先等待人工确认，再并行执行两个分支，最后汇合”：

```text
approval-gate → build-plan → summary ───┐
                            readiness ──┴→ join → finalize
```

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

`approval-gate` 是 Workflow 内可恢复的 idle node，不是阻塞 Activity。确认前 projection 显示 `waiting-for-user`；确认后，recorded `BuildPlan` result 创建两个 stable-key runtime branch。

## 这个 Sample 教什么

完成后，你应该能说明：

- Workflow 如何进入 `waiting-for-user`，并在 Worker restart 后继续等待。
- 人工 `confirm` action 为什么必须经过 org Gateway。
- 两个 runtime branch 如何并行启动并在 join 汇合。
- action delivery 和 Workflow acceptance 为什么是两个不同状态。

## 业务代码

- `definition.go`：`AwaitConfirmation`、runtime fork/join 与 projection。
- `activities.go`：plan、branch 和 finalize 业务逻辑。

Sample 不手写 Signal、projection 或 raw Temporal 注册。人工 action 只通过 org Gateway 提交。

## 测试

```sh
make test
make vet
make verify
```

测试证明确认前不调度 Activity、两个 branch 在等待结果前都已启动、join 只在二者完成后运行。

## 构建、push 或加载到 kind

本地构建：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make image VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

push 到自己的 registry：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make push \
  IMAGE_REPOSITORY=registry.example.com/team/parallel-confirmation-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

或加载到本地 `kind-org`：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

成功后复制 `IMAGE_DIGEST=<repository>@sha256:...`。当前目录就是完整 Docker build context；不需要 org 根源码或根 Makefile。

## 在 org 中运行

1. 在 Console 创建 Worker `parallel-confirmation-worker`。
2. 用上一步 digest 发布 Version，并等待 SDK registration、poller 与 probe。
3. 触发 `ParallelConfirmationWorkflow`。Trigger editor 默认使用 YAML，可输入 `subject: release notes`；切换为 JSON 时输入 `{"subject":"release notes"}`。只读 schema 仅供核对复杂 payload，不生成标准表单字段。
4. 可选填写 Run description，说明这一次审批为何发起；它不属于 payload，也不要包含 Secret。
5. Run detail 先显示 `approval-gate = waiting-for-user` 和 `confirm` action。
6. 提交确认后，观察两个 running branch、join 与 finalize 依次完成。

Gateway 会校验 Tenant、permission、input schema、projection revision 与 `Idempotency-Key`。终端用户不直连 Temporal，也不直接发送 Signal。

Org SDK 在 Worker 启动时从 typed Definition 生成 contract 并自动注册。Console 不接收用户提供的 contract 文件。

## 发布输入与平台配置

org 平台注入执行连接、候选 Pod identity 和一次性注册材料。用户不手填这些值，不创建 credential 文件，也不把它们写进 image。

用户只维护业务 Definition/Activities 和 image；Version 的发布字段由 org API 承接，详见 [发布 WorkerVersion](https://github.com/wu8685/org/blob/main/docs/api/publish-worker-version.md)。真实 write Activity 必须声明 stable idempotency key 或 reconciliation/compensation policy。

本地演示的 resource 可以从 `100m` CPU、`128Mi` memory 起步。production 应根据两个并行 Activity 的并发数、峰值内存和实际耗时重新测量并设置 requests/limits，不要直接照搬示例值。
