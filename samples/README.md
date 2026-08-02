# Org SDK Sample

三个官方 Sample 分别展示顺序执行、人工确认与运行时分支。它们已经过项目测试；如果你的目标是第一次体验 org，可以直接构建并运行，不需要先替 Sample 执行测试。

> 产品术语遵循 [org glossary](../docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 先选择想看的结果

| 顺序 | Sample | 适合了解什么 | Console 中会看到什么 |
|---|---|---|---|
| 1 | [Hello](hello/README.md) | 最小 Worker 与顺序 Activity | 两个 Activity 依次运行，约 10 秒后得到 `Hello, Codex!` |
| 2 | [Parallel confirmation](parallel-confirmation/README.md) | 等待人工确认与并行汇合 | `waiting-for-user`，确认后两个分支同时运行并 join |
| 3 | [Dynamic decision](dynamic-decision/README.md) | 根据 Activity result 选择路径 | 被选分支执行，未选分支显示 `skipped` |

第一次使用建议从 Hello 开始。完整的平台启动、发布和触发步骤见[本地快速上手](../docs/getting-started.md)。

## 首次体验：直接运行官方 Sample

完整本地体验需要已启动的 org Console、Temporal、Docker daemon 和 `kind-org` cluster。单独执行 `make kind-load` 时，宿主机需要 Docker、kind 和一个已存在的目标 cluster；脚本使用 kind node 内的 container runtime 工具，不要求宿主机安装 `crictl`。完整教程还会使用 `kubectl` 检查平台状态。

以 Hello 为例：

```sh
cd samples/hello
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

成功后复制命令输出的不可变引用：

```text
IMAGE_DIGEST=<repository>@sha256:...
```

然后在 Console 中创建对应 Worker、录入 WorkerVersion，并触发 Workflow。每个 Sample README 都列出了输入和应观察到的状态变化。

## 每个目录都可以独立复制

每个 Sample 目录都包含自己的：

- Go module；
- Definition、Activities 和 tests；
- Makefile；
- Dockerfile 与 build/push/kind scripts。

构建只使用当前目录，不依赖 org 根 Makefile，也不 import org internal package。复制到独立 repository 后，仍可测试、构建和发布。

## 修改后验证

只有复制或修改 Sample 后，才需要先验证自己的改动：

```sh
make verify
```

`make verify` 会运行当前 Sample 的 Go tests 和 `go vet`。这些检查验证业务行为、Workflow projection 与生成的 contract；它们不替代 Docker、kind、Temporal 或 Console 的集成验证。

推荐的 Worker 开发路径是：

```text
复制 Sample
  → 修改 Definition、Activities、输入输出和 tests
  → make verify
  → 构建不可变 image
  → 发布新的 WorkerVersion
  → 在 Console 观察 Run
```

## 构建到自己的 registry

需要推送到 registry 时，先完成 Docker login，再在对应 Sample 目录运行：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make push \
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

脚本不保存 registry credential。发布时始终使用输出的 `IMAGE_DIGEST`，不要使用可变 tag。通用发布字段与平台边界见[发布 WorkerVersion](../docs/api/publish-worker-version.md)。
