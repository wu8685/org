# Org SDK Sample 学习路径

三个 Sample 分别回答一个问题：最小 Workflow 怎么写、怎样等待人工确认、怎样根据运行结果选择分支。建议按顺序阅读，不要一开始就进入最复杂的 dynamic-decision。

## 先选你要学习的能力

| 顺序 | Sample | 核心问题 | Console 中会看到什么 |
|---|---|---|---|
| 1 | [Hello](hello/README.md) | 一个最小 Worker 需要哪些代码 | 两个顺序 Activity，最后进入 `completed` |
| 2 | [Parallel confirmation](parallel-confirmation/README.md) | Workflow 如何等待人、恢复并并行执行 | `waiting-for-user`，确认后两个分支同时运行并 join |
| 3 | [Dynamic decision](dynamic-decision/README.md) | 运行结果如何决定后续路径 | selected 分支执行，未选分支显示 `skipped` |

如果还没有完成第一次发布，先回到 [本地快速上手](../docs/getting-started.md)。它会带你完整跑通 Hello。

## 每个 Sample 都是独立 Worker repository

每个目录都拥有自己的：

- Go module
- Definition 与 Activities
- tests
- Makefile
- Dockerfile 和 build/push/kind scripts

进入目录后即可测试、构建、push 或加载到 kind，不依赖 org 根 Makefile，也不 import org internal package。

## 共同命令

以下命令以 Hello 为例；切换到另一个 Sample 目录后用法相同：

```sh
cd samples/hello # 或另一个 Sample 目录
make test
make verify
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

`make kind-load` 会输出：

```text
IMAGE_DIGEST=<repository>@sha256:...
```

使用自己的 registry 时：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make push \
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

Sample repository 不保存 registry credential，也不保存 control-plane publish body。

## 从代码看到 Console

阅读每个 Sample 时，可以沿着同一条链路：

```text
definition.go
  → 声明 Workflow、Activity、node 和 action

activities.go
  → 实现业务工作

tests
  → 验证顺序、分支、等待和结果

make kind-load
  → 得到 immutable image digest

org Console
  → 发布 Version，触发 Workflow，观察 Run
```

Org SDK 在 Worker 启动时根据 typed Definition 生成 contract 并自动注册。Sample 不要求用户维护 manifest 文件，也不直接处理平台 routing 或 Temporal credential。

## 发布时还需要什么

在 org Console 创建对应 Worker，并使用 `IMAGE_DIGEST` 发布 Version。候选 Pod 会完成 registration、polling 和 probe，成功后成为 Ready / Current。

完整字段和 digest-only 请求契约见 [发布 WorkerVersion](https://github.com/wu8685/org/blob/main/docs/api/publish-worker-version.md)。产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)。

## 安全边界

- 平台注入 bootstrap、Temporal 连接和 routing 配置；Sample 不保存这些值。
- 不要把 Secret 写进 image、Workflow input、projection、log 或 Audit。
- 真实 write Activity 必须使用稳定的 idempotency key，或声明 reconciliation/compensation policy。
