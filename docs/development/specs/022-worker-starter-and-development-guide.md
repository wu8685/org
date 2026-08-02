# Worker Starter 与开发教程规格

> 产品术语遵循 [org glossary](../../user/architecture/glossary.md)。

## 状态

**Approved — implementation authorized by the user on 2026-08-02.**

## 目标

用户跑通 Hello Sample 后，可以沿一篇连续的教程创建自己的 Worker，完成修改、测试、构建、发布和运行。教程使用专门的 Worker Starter，不要求复制任何 Sample，也不要求从空目录手写工程文件。

三个 Sample 继续演示 Org SDK 能力：Hello 展示顺序执行，Parallel Confirmation 展示人工确认和并行，Dynamic Decision 展示运行时分支。Sample 不承担项目脚手架职责。

## 用户路径

```text
完成本地快速上手
→ 复制 templates/worker 到自己的 repository
→ 修改 module、Workflow 和 Activity 身份
→ 修改 input、output 与 Activity 业务代码
→ 使用 Org SDK test kit 验证 Workflow
→ 构建 image 并取得 immutable digest
→ 在 Console 发布 WorkerVersion
→ 启动 Workflow Run 并查看结果
```

`docs/user/create-your-worker.md` 必须完整拥有这条路径。用户不需要在教程、Sample README 和 SDK 源码之间来回查找下一步。

## Worker Starter 契约

`templates/worker/` 是可复制的独立 Go repository，至少包含：

```text
README.md
Makefile
go.mod / go.sum
Dockerfile
.dockerignore
types.go
activities.go
definition.go
cmd/worker/main.go
scripts/build-image.sh
scripts/push-image.sh
scripts/kind-load.sh
*_test.go
```

约束：

- Starter 使用 versioned public Org SDK，不使用 parent `replace`，复制后执行 `GOWORK=off make verify` 必须成功。
- 默认实现只有一个可运行的 Activity 和一个完成节点；不包含教学延迟、人工 action、并行或动态分支。
- 默认代码名称集中在 `types.go`，教程明确指出需要修改的 module path、Workflow name、Activity ID 和业务类型。Worker name 在 Console 创建，不写入 SDK contract。
- Worker 业务代码不直接 import Temporal SDK，不保存 manifest、publish request、platform credential 或 routing 配置。
- Makefile 保留 `test`、`vet`、`verify`、`image`、`push` 和 `kind-load`，构建只使用 Starter 根目录作为 Docker context，并输出 `IMAGE_DIGEST`。
- Starter README 只说明如何开始和去哪里继续，不复制完整教程。

## 用户文档职责

- `docs/user/create-your-worker.md`：从 Starter 到首次 Run 的完整教程。
- `docs/user/write-your-worker.md`：Org SDK 开发参考，解释 Definition、Workflow、Activity、policy、测试与 hosted 入口；不再承担脚手架、构建和 Console 教程。
- `docs/user/README.md` 与 `docs/README.md`：先引导用户跑通 Sample，再进入创建 Worker 教程，SDK 参考放在教程之后。
- `samples/README.md` 与各 Sample README：只描述对应能力、运行操作和观察结果；不要求用户复制 Sample 创建项目。

## TDD 与验收

1. 先增加失败的 Starter repository contract test，检查文件、versioned SDK、无 parent dependency、无 raw Temporal import，以及复制后的 `make verify`。
2. 先增加失败的用户路径测试，要求导航页、创建教程、SDK 参考和 Samples 的职责符合本规格。
3. 创建 Starter，并用其自身 module 生成 `go.sum`、运行 `make verify`。
4. 编写创建教程，重构 SDK 参考和导航。
5. 运行 root docs tests、Starter tests、相关 Go tests和文档链接检查。
6. 人工检查用户文档，删除背景说明、否定式提醒和无操作价值的段落。
