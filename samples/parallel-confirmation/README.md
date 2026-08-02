# Parallel Confirmation

## 这个 Sample 展示什么

Parallel Confirmation 先等待用户确认，确认后同时执行两个分支，最后汇合：

```text
等待确认 → build-plan → summary ───┐
                         readiness ──┴→ finalize
```

运行时会先看到 `waiting-for-user`，提交 `confirm` 后再看到两个分支并行执行。

## 运行 Sample

先按[本地快速上手](https://github.com/wu8685/org/blob/main/docs/user/getting-started.md)启动本地环境，然后在本目录运行：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

复制输出的 `IMAGE_DIGEST`。

### 1. 发布版本

> 在 Console 创建 Worker `parallel-confirmation-worker`，点击“录入版本”。
>
> 填写版本号、版本说明和 `IMAGE_DIGEST`，点击“开始发布”，等待发布成功。

### 2. 启动 Workflow

> 选择 `ParallelConfirmationWorkflow`，使用默认的 YAML 输入：
>
> `subject: release notes`
>
> Run description 可选，可以留空。

### 3. 确认并查看结果

> 打开 Run detail：
>
> - 看到 `waiting-for-user` 后，提交 `confirm`；
> - 两个分支会同时进入 `running`，各自约需 2–5 秒；
> - 两个分支完成后，Workflow 汇合并结束。
