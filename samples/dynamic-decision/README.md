# Dynamic Decision Org SDK Worker

这个独立 Worker repository 演示由 recorded Activity result 决定 runtime if/else：

```text
determine-route ─┬→ concise-branch  ─┐
                 └→ detailed-branch ─┴→ finalize
```

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

只执行 selected branch；未选 candidate 仍出现在 dynamic semantic projection 中，状态为 `skipped`，reason code 为 `route-not-selected`。

## 这个 Sample 教什么

完成后，你应该能说明：

- Activity result 如何成为 deterministic 分支依据。
- 为什么两个候选节点都出现在 projection 中，但只执行一个 Activity。
- 未选分支为什么使用 `skipped`，而不是从 DAG 中消失。
- 两条 runtime path 如何汇合到共同 finalize node。

## 业务代码

- `definition.go`：recorded route、selected/skipped 节点和共同 finalize。
- `activities.go`：route、concise、detailed 与 finalize 业务逻辑。

Workflow 不能直接访问外部服务。它只依据 Temporal 已记录的 Activity result 作 deterministic 决策。

## 测试

```sh
make test
make vet
make verify
```

测试分别覆盖 `concise` 与 `detailed`，并证明未选 Activity handler 不会执行。

## 构建、push 或加载到 kind

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make image VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

push 到自己的 registry：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make push \
  IMAGE_REPOSITORY=registry.example.com/team/dynamic-decision-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

或加载到本地 `kind-org`：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

成功后复制 `IMAGE_DIGEST=<repository>@sha256:...`。构建只使用当前 repository；org 私有源码不是前置条件。

## 在 org 中运行

1. 在 Console 创建 Worker `dynamic-decision-worker`。
2. 用 digest 发布 Version，并等待 SDK registration、poller 与 probe。
3. 触发 `DynamicDecisionWorkflow`。Trigger editor 默认使用 YAML，可输入 `mode: concise` 和 `subject: release notes`；切换为 JSON 时输入 `{"mode":"concise","subject":"release notes"}`。只读 schema 只描述业务 payload，不生成固定字段。
4. 可选填写 Run description，说明为何选择这次演示输入；它不属于 payload，也不要包含 Secret。
5. 先观察 `determine-route=running`；route 确定后，观察 `concise-branch=running`、`detailed-branch=skipped`，最后 `finalize=running` 并完成。
6. 再用 `mode=detailed` 触发独立 Run，观察相反路径。

## 教学演示延迟

每条合法 route 只有 3 个实际 Activity 使用独立随机 2–5 秒延迟：`DetermineRoute`、被选中的 branch、`Finalize`。未选 branch 立即成为 `skipped`，不会执行 handler、取随机值或等待；它不能伪装成 `running`。每个 Run 通常约 6–15 秒再加少量调度开销即可完成。

延迟仅用于教学演示，让 Console 有时间显示运行时决策过程。production Worker 不应照搬人为 sleep，应让 projection 反映真实 Activity 生命周期。取消 Run 时，Activity delay 会通过 context 及时中断。

Org SDK 从 typed Definition 生成 contract 并在启动时自动注册。Console 只读展示 contract 和动态 DAG，不从 Temporal Event History 猜路径。

## 发布输入与平台配置

org 平台注入执行连接、候选 Pod identity 和一次性注册材料。用户不手填这些值，也不把 credential 或 routing 写进 image。

用户只维护业务 Definition/Activities 和 image；Version 的发布字段由 org API 承接，详见 [发布 WorkerVersion](https://github.com/wu8685/org/blob/main/docs/api/publish-worker-version.md)。Secret 或敏感 input 不得进入 projection、log 或 Audit。

本地演示的 resource 可以从 `100m` CPU、`128Mi` memory 起步。production 应根据各分支 Activity 的实际 CPU、峰值内存和并发画像重新测量并设置 requests/limits；未执行的 `skipped` 分支不代表可以忽略被选分支的容量规划。
