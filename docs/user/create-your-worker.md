# 创建你的第一个 Worker

这篇教程从 Worker Starter 开始，最后在本地 org 中运行你自己的 Workflow。开始前，请先完成[本地快速上手](getting-started.md)，并保持 Temporal、org Console 和 `kind-org` 运行。

## 1. 创建项目

在 org repository 根目录运行：

```sh
cp -R templates/worker ../my-worker
cd ../my-worker
go mod edit -module github.com/acme/my-worker
```

把 `github.com/acme/my-worker` 换成你的 module path，然后在 `cmd/worker/main.go` 中使用相同的 import：

```go
worker "github.com/acme/my-worker"
```

先修改这些文件：

```text
types.go            Workflow、Activity 名称和业务输入输出
activities.go       业务逻辑
definition.go       Workflow 的节点和执行顺序
definition_test.go  Workflow 测试
cmd/worker/main.go  Worker 入口
```

## 2. 改成你的 Workflow

### 设置名称和输入输出

在 `types.go` 修改：

```go
const (
	WorkflowName      = "MyWorkflow"
	processActivityID = "process"
)
```

`WorkflowName` 是启动 Run 时选择的 Workflow。Activity ID 会显示为 Run detail 中的节点。Worker 名称在发布版本时创建。

接着把 `Input` 和 `Result` 改成自己的业务结构。Org SDK 会根据 Go 类型和 `json` tag 生成 input/output schema。

```go
type Input struct {
	Value string `json:"value"`
}

type Result struct {
	Value string `json:"value"`
}
```

### 编写业务逻辑

在 `activities.go` 修改 `Process`。这里可以请求外部服务、读写数据库或访问文件：

```go
func Process(input Input) (Result, error) {
	// 在这里实现业务逻辑。
	return Result{Value: input.Value}, nil
}
```

Starter 中的 ActivityPolicy 是 `SideEffectNone`。读取外部数据时改为 `SideEffectRead`；写入外部系统时改为 `SideEffectWrite`，并配置幂等性或 reconciliation。具体字段见 [Activity](write-your-worker.md#activity)。

`definition.go` 已经把这个 Activity 接入 Workflow：

```text
process → completed
```

需要更多步骤时，在这里声明新的 Activity 和节点，再用 `ExecuteActivity` 定义执行顺序。并行、人工确认和动态分支见 [Org SDK 开发参考](write-your-worker.md)。

### 更新测试

修改 `activities_test.go` 中的业务输入和断言，再修改 `definition_test.go` 中的 Workflow input 和预期结果。

运行：

```sh
make verify
```

测试通过后再继续构建。

## 3. 构建 image

先建立 Git repository，记录这次构建对应的 revision：

```sh
git init
git add .
git commit -m "Initialize Worker"
SOURCE_REVISION=$(git rev-parse --short=12 HEAD)
```

把 image 加载到本地 `kind-org`：

```sh
make kind-load \
  IMAGE_REPOSITORY=org.local/my-worker \
  VERSION=0.1.0 \
  COMMIT="$SOURCE_REVISION"
```

命令完成后会打印：

```text
IMAGE_TAG=org.local/my-worker:0.1.0-<commit>
IMAGE_DIGEST=org.local/my-worker@sha256:<digest>
```

复制完整的 `IMAGE_DIGEST`。

## 4. 发布版本

> 在 Console 打开 Workers，创建 Worker `my-worker`，点击“录入版本”。
>
> 填写版本号 `0.1.0`、版本说明和 `IMAGE_DIGEST`，点击“开始发布”，等待发布成功。

> 通过 API 发布时，使用[发布 WorkerVersion](api/publish-worker-version.md)中的请求格式。

## 5. 启动 Workflow

> 选择 `MyWorkflow`，使用默认的 YAML 输入：`value: first run`。
>
> Run description 可选，可以留空。启动 Workflow。

## 6. 观察结果

> 打开 Run detail。
>
> `process` 和 `completed` 最终都显示为 `completed`，结果为 `value: first run`。

以后修改 Worker 时，重新运行 `make verify` 和 `make kind-load`，使用新的 version 发布即可。

> 产品术语遵循 [org glossary](architecture/glossary.md)：用户隔离边界统一称 Tenant。
