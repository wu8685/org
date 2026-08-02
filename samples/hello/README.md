# Hello

## 这个 Sample 展示什么

Hello 接收一个名字，依次执行两个步骤并生成问候语：

```text
prepare-greeting → compose-greeting → completed
```

输入 `name: Codex`，最终得到 `Hello, Codex!`。

## 运行 Sample

先按[本地快速上手](https://github.com/wu8685/org/blob/main/docs/user/getting-started.md)启动本地环境，然后在本目录运行：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的 7–64 位 hexadecimal source revision
make kind-load \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

复制输出的 `IMAGE_DIGEST=org.local/hello-worker@sha256:...`。

### 1. 发布版本

> 在 Console 创建 Worker `hello-worker`，点击“录入版本”。
>
> 填写版本号、版本说明和 `IMAGE_DIGEST`，点击“开始发布”，等待发布成功。

### 2. 启动 Workflow

> 选择 `HelloWorkflow`，使用默认的 YAML 输入：
>
> `name: Codex`
>
> Run description 可选，可以留空。

### 3. 查看结果

> 打开 Run detail：
>
> - `prepare-greeting` 很快完成；
> - `compose-greeting` 保持约 10 秒 `running`；
> - 最终结果显示 `Hello, Codex!`。
