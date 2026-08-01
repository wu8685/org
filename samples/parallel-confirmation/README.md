# Parallel Confirmation Org SDK Worker

这个独立Worker repository演示“先等待人工确认，再并行执行两个分支，最后汇合”：

```text
approval-gate → build-plan → summary ───┐
                            readiness ──┴→ join → finalize
```

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

`approval-gate`是Workflow内可恢复的idle node，不是阻塞Activity。确认前projection显示`waiting-for-user`；确认后，recorded `BuildPlan` result创建两个stable-key runtime branch。

## 业务代码

- `definition.go`：`AwaitConfirmation`、runtime fork/join与projection；
- `activities.go`：plan、branch和finalize业务逻辑。

Sample不手写Signal、projection或raw Temporal注册。人工action只通过org Gateway提交。

## 测试

```sh
make test
make vet
make verify
```

测试证明确认前不调度Activity、两个branch在等待结果前都已启动、join只在二者完成后运行。

## Build、push或kind-load

本地build：

```sh
make image VERSION=2026.08.1 COMMIT=$(git rev-parse --short=12 HEAD)
```

push自己的registry：

```sh
make push \
  IMAGE_REPOSITORY=registry.example.com/team/parallel-confirmation-worker \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

或加载本地`kind-org`：

```sh
make kind-load VERSION=2026.08.1 COMMIT=$(git rev-parse --short=12 HEAD)
```

成功后复制`IMAGE_DIGEST=<repository>@sha256:...`。当前目录就是完整Docker build context；不需要org根源码或根Makefile。

## 在org中运行

1. 在Console创建Worker `parallel-confirmation-worker`；
2. 用上一步digest发布Version，并等待SDK registration、poller与probe；
3. 触发`ParallelConfirmationWorkflow`；
4. Run detail先显示`approval-gate = waiting-for-user`和`confirm` action；
5. 提交确认后，观察两个running branch、join与finalize依次完成。

Gateway会校验Tenant、permission、input schema、projection revision与`Idempotency-Key`。终端用户不直连Temporal，也不直接发送Signal。

Org SDK在Worker启动时从typed Definition生成contract并自动注册。Console不接收用户提供的contract文件。

## 哪些配置由平台注入

org平台注入bootstrap endpoint/token、Pod identity、Temporal连接、Task Queue、Worker Deployment和Build ID。用户不手填这些值，不创建credential文件，也不把它们写进image。

用户只维护业务Definition/Activities、image与release输入。发布body示例见`config/release.example.json`。真实write Activity必须声明stable idempotency key或reconciliation/compensation policy。
