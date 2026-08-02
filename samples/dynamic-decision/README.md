# Dynamic Decision

## 这个 Sample 展示什么

Dynamic Decision 根据输入的 `mode` 选择一条分支：

```text
determine-route ─┬→ concise-branch  ─┐
                 └→ detailed-branch ─┴→ finalize
```

被选中的分支会执行，另一条分支显示为 `skipped`。

## 运行 Sample

先按[本地快速上手](https://github.com/wu8685/org/blob/main/docs/user/getting-started.md)启动本地环境，然后在本目录运行：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

复制输出的 `IMAGE_DIGEST`。

### 1. 发布版本

> 在 Console 创建 Worker `dynamic-decision-worker`，点击“录入版本”。
>
> 填写版本号、版本说明和 `IMAGE_DIGEST`，点击“开始发布”，等待发布成功。

### 2. 启动 Workflow

> 选择 `DynamicDecisionWorkflow`，使用默认的 YAML 输入：
>
> `mode: concise`
>
> `subject: release notes`
>
> Run description 可选，可以留空。

### 3. 查看分支结果

> 打开 Run detail：
>
> - `concise-branch` 进入 `running`；
> - `detailed-branch` 显示 `skipped`；
> - 约 6–15 秒后，Workflow 完成。

## 尝试其他输入

- 使用 `mode: detailed`，可以看到另一条分支运行。
- 使用 `mode: automatic`，Run 会显示 `invalid_route`：`Unsupported mode. Choose concise or detailed.`
