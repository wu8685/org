# Hello Org SDK Worker

Hello 是最小的 Org SDK Worker。它接收一个名字，用两个顺序 Activity 生成问候语，并在 Console 中展示完整执行过程：

```text
prepare-greeting → compose-greeting → completed
```

输入 `name: Codex`，最终得到 `Hello, Codex!`。

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 首次体验会看到什么

触发 `HelloWorkflow` 后，打开 Run detail：

1. `prepare-greeting` 很快变为 `completed`；
2. `compose-greeting` 保持约 10 秒 `running`；
3. `completed` 最终完成，结果显示 `Hello, Codex!`。

约 10 秒的等待是 Sample 故意加入的教学演示延迟，目的是让你看清 Activity 状态变化，不是平台卡住。production Worker 不应照搬人为 sleep；复制本 Sample 时应删除该 option，让 projection 反映真实业务耗时。

## 首次体验：直接构建并运行

官方 Sample 已经通过项目测试。第一次体验不需要先运行 `make test` 或 `make vet`；请按[本地快速上手](https://github.com/wu8685/org/blob/main/docs/getting-started.md)启动 Console、Temporal 和 `kind-org`。

`make kind-load` 的宿主机依赖是 Docker、kind 和已存在的目标 cluster，不要求宿主机安装 `crictl`。在本目录运行：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

复制输出的 `IMAGE_DIGEST=org.local/hello-worker@sha256:...`，然后：

1. 在 Console 创建 Worker `hello-worker`；
2. 点击“录入版本”，填写 Version description、`IMAGE_DIGEST`、`100m` CPU 和 `128Mi` memory，再点击“开始发布”；
3. 等待 SDK registration、poller 与 probe 完成；
4. 触发 `HelloWorkflow`。Trigger editor 默认使用 YAML，输入 `name: Codex`；切换为 JSON 时输入 `{"name":"Codex"}`。只读 schema 用于核对业务 payload，不会生成固定输入字段；
5. Run description 为可选说明，不属于 Workflow payload，也不要包含 Secret；
6. 按上一节的时间线观察 Run detail。

## 代码与运行过程如何对应

- `types.go`：Worker、Workflow 名以及 input/output 类型；
- `definition.go`：typed Definition、节点依赖、retry/timeout policy 和教学延迟；
- `activities.go`：输入校验与问候语生成；
- `cmd/worker/main.go`：加载平台注入配置并启动 Worker；
- `*_test.go`：验证业务行为、执行顺序、projection 和 contract。

Sample 不 import raw Temporal SDK，不手写 projection 或平台 routing。Org SDK 根据 typed Definition 生成 contract，并在 Worker 启动时自动注册。

## 复制成自己的 Worker

不要只修改 `activities.go`。至少逐项检查：

1. 修改 `go.mod` module，并同步更新 `cmd/worker/main.go` 的 import；
2. 修改 `types.go` 中的 WorkerName、WorkflowName、input 和 output；
3. 修改 Definition 名、Activity ID、节点 ID 与依赖关系；
4. 用自己的业务逻辑替换 `activities.go`；
5. 删除 `WithComposeGreetingDelay` 教学延迟；
6. 修改 Makefile 的默认 image repository 和 Dockerfile 的 OCI source label；
7. 修改 tests，使其描述你的业务行为；
8. 使用新的 Version 构建并发布，不要覆盖已经提交的 WorkerVersion。

## 修改后验证

复制或修改代码后运行：

```sh
make verify
```

它会执行 Go tests 和 `go vet`。测试使用 no-op sleeper，因此不会真的等待约 10 秒；测试断言会验证 `Hello, Codex!`、三个节点的最终状态和生成的 contract。通过后再执行 `make kind-load` 或 `make push`。

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
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

构建 context 只有当前 repository。脚本不保存 registry credential；发布时使用命令输出的 immutable `IMAGE_DIGEST`。通用字段、安全边界和 Secret reference 见[发布 WorkerVersion](https://github.com/wu8685/org/blob/main/docs/api/publish-worker-version.md)。
