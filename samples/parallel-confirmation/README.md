# Parallel Confirmation Org SDK Worker

这个 Sample 展示一个长期 Workflow 如何先等待人工确认，再并行执行两个分支，最后汇合：

```text
approval-gate → build-plan → summary ───┐
                            readiness ──┴→ join → finalize
```

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 首次体验会看到什么

触发 `ParallelConfirmationWorkflow` 后，Run detail 会按以下顺序变化：

1. `approval-gate` 显示 `waiting-for-user`，并提供 `confirm` action；
2. 确认前不会调度 Activity，Worker restart 后仍能继续等待；
3. 提交确认后，`BuildPlan` 进入 `running`；
4. `summary` 与 `readiness` 两个 runtime branch 同时进入 `running`，完成顺序可能不同；
5. 两个分支都完成后，join 与 `Finalize` 依次完成。

`approval-gate` 是 Workflow 中可恢复的 idle node，不是占用线程等待的 Activity。action 由 org Gateway 接收；“请求已送达”和“Workflow 已接受”是两个不同状态。

## 首次体验：直接构建并运行

官方 Sample 已经通过项目测试。第一次体验不需要先运行 test 或 vet；请先按[本地快速上手](https://github.com/wu8685/org/blob/main/docs/getting-started.md)启动 Console、Temporal 和 `kind-org`。

`make kind-load` 的宿主机依赖是 Docker、kind 和已存在的目标 cluster，不要求宿主机安装 `crictl`。在本目录运行：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

复制输出的 `IMAGE_DIGEST`，然后：

1. 在 Console 创建 Worker `parallel-confirmation-worker`；
2. 点击“录入版本”，用 digest 发布 WorkerVersion，并等待 SDK registration、poller 与 probe；
3. 触发 `ParallelConfirmationWorkflow`。Trigger editor 默认使用 YAML，输入 `subject: release notes`；切换为 JSON 时输入 `{"subject":"release notes"}`。只读 schema 用于核对 payload，不会生成固定输入字段；
4. Run description 是可选说明，不属于 payload，也不要包含 Secret；
5. 按上一节时间线先观察等待状态，再提交 `confirm`。

## 为什么状态变化需要几秒

确认后的每个实际 Activity 都有独立随机 2–5 秒延迟：`BuildPlan` 一次、两个 `ExecuteBranch` 各一次、`Finalize` 一次。随机值彼此独立，所以两个并行分支可能先后完成；确认后通常约 6–15 秒再加少量调度开销。

延迟仅用于教学演示，让 Console 有时间显示 `running`。production Worker 不应照搬人为 sleep；取消 Run 时，Activity delay 会通过 context 及时中断。本地演示可从 `100m` CPU、`128Mi` memory 起步，production 应按实际并发、峰值内存与 Activity 耗时重新测量。

## 代码与运行过程如何对应

- `types.go`：Worker、Workflow 名以及 input/output；
- `definition.go`：`AwaitConfirmation`、runtime fork/join、projection 和教学延迟；
- `activities.go`：plan、两个 branch 与 finalize 业务逻辑；
- `cmd/worker/main.go`：加载平台配置并启动 Worker；
- `*_test.go`：验证确认、并行、join、projection 与 contract。

Sample 不手写 Signal、projection 或 raw Temporal 注册。人工 action 只能通过 org Gateway 提交。

Org SDK 会从 typed Definition 生成 contract，并在 Worker 启动时自动注册。Temporal 连接、routing 和候选 Pod identity 由平台注入，不是用户维护的 `.env` 或 image 配置。

## 修改后验证

复制或修改 Sample 后运行：

```sh
make verify
```

它会执行 Go tests 和 `go vet`，证明确认前不调度 Activity、两个 branch 在等待结果前都已启动、join 只在二者完成后运行。通过后再构建和发布新的 WorkerVersion。

## 构建或推送 image

只在本机生成 image：

```sh
SOURCE_REVISION=abcdef1
make image VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

推送到自己的 registry：

```sh
SOURCE_REVISION=abcdef1
make push \
  IMAGE_REPOSITORY=registry.example.com/team/parallel-confirmation-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

当前目录就是完整 build context。脚本不保存 registry credential；发布时使用输出的 immutable `IMAGE_DIGEST`。通用发布字段、Gateway 校验与 Secret 边界见[发布 WorkerVersion](https://github.com/wu8685/org/blob/main/docs/api/publish-worker-version.md)。
