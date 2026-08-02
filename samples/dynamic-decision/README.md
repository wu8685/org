# Dynamic Decision Org SDK Worker

这个 Sample 展示 Workflow 如何根据已记录的 Activity result，在运行时选择一条路径：

```text
determine-route ─┬→ concise-branch  ─┐
                 └→ detailed-branch ─┴→ finalize
```

只执行被选分支；未选候选仍显示在 dynamic semantic projection 中，状态为 `skipped`，reason code 为 `route-not-selected`。

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 首次体验会看到什么

用 `mode: concise` 触发 `DynamicDecisionWorkflow` 后：

1. `determine-route` 进入 `running`；
2. route 确定后，`concise-branch` 进入 `running`；
3. `detailed-branch` 立即显示 `skipped`，不会执行 Activity handler；
4. 被选分支完成后，`finalize` 运行并完成整个 Run。

再用 `mode: detailed` 启动一个独立 Run，会看到相反路径。这两个候选节点不会因为未被选择而从 DAG 消失，因此用户仍能理解完整决策空间。

## 首次体验：直接构建并运行

官方 Sample 已经通过项目测试。第一次体验不需要先运行 test 或 vet；请先按[本地快速上手](https://github.com/wu8685/org/blob/main/docs/getting-started.md)启动 Console、Temporal 和 `kind-org`。

`make kind-load` 的宿主机依赖是 Docker、kind 和已存在的目标 cluster，不要求宿主机安装 `crictl`。在本目录运行：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

复制输出的 `IMAGE_DIGEST`，然后：

1. 在 Console 创建 Worker `dynamic-decision-worker`；
2. 点击“录入版本”，用 digest 发布 WorkerVersion，并等待 SDK registration、poller 与 probe；
3. 触发 `DynamicDecisionWorkflow`。Trigger editor 默认使用 YAML，输入 `mode: concise` 和 `subject: release notes`；切换为 JSON 时输入 `{"mode":"concise","subject":"release notes"}`。只读 schema 用于核对 payload，不会生成固定输入字段；
4. Run description 是可选说明，不属于 payload，也不要包含 Secret；
5. 按上一节时间线观察 selected 与 `skipped` 分支。

## 再观察一个安全失败

输入 `mode: automatic` 会让 `Determine route` 失败。Runs list 与 detail 显示安全错误 code `invalid_route` 和 message `Unsupported mode. Choose concise or detailed.`。

错误不会回显非法输入，也不会展示 raw error、stack 或底层 execution history；有权限的维护者可以另行打开 advanced diagnostics。这条路径用于说明平台如何向普通用户提供可行动的安全错误摘要。

## 为什么状态变化需要几秒

每条合法 route 只有三个实际 Activity 使用独立随机 2–5 秒延迟：`DetermineRoute`、被选中的 branch 和 `Finalize`。未选 branch 立即成为 `skipped`，不会取随机值或等待。每个 Run 通常约 6–15 秒再加少量调度开销。

延迟仅用于教学演示，让 Console 有时间显示 `running` 和运行时决策。production Worker 不应照搬人为 sleep；取消 Run 时，Activity delay 会通过 context 及时中断。本地演示可从 `100m` CPU、`128Mi` memory 起步，production 应按被选分支的实际 CPU、峰值内存和并发重新测量。

## 代码与运行过程如何对应

- `types.go`：Worker、Workflow 名和包含 `mode` 的 input；
- `definition.go`：recorded route、selected/skipped 节点、汇合和教学延迟；
- `activities.go`：route、concise、detailed 与 finalize 业务逻辑；
- `cmd/worker/main.go`：加载平台配置并启动 Worker；
- `*_test.go`：验证两条 route、非法输入、projection 与 contract。

Workflow 不直接访问外部服务，只依据 Temporal 已记录的 Activity result 作 deterministic 决策。Console 展示 SDK 生成的 contract 和动态 DAG，不从 Temporal Event History 猜测路径。

Org SDK 会在 Worker 启动时自动注册 contract。Temporal 连接、routing 和候选 Pod identity 由平台注入，不是用户维护的 `.env` 或 image 配置。

## 修改后验证

复制或修改 Sample 后运行：

```sh
make verify
```

它会执行 Go tests 和 `go vet`，覆盖 `concise`、`detailed` 和非法 route，并证明未选 Activity handler 不会执行。通过后再构建和发布新的 WorkerVersion。

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
  IMAGE_REPOSITORY=registry.example.com/team/dynamic-decision-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

当前目录就是完整 build context。脚本不保存 registry credential；发布时使用输出的 immutable `IMAGE_DIGEST`。通用发布字段与 Secret 边界见[发布 WorkerVersion](https://github.com/wu8685/org/blob/main/docs/api/publish-worker-version.md)。
