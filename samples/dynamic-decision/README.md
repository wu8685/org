# Dynamic Decision Org SDK Worker

这个独立Worker repository演示由recorded Activity result决定runtime if/else：

```text
determine-route ─┬→ concise-branch  ─┐
                 └→ detailed-branch ─┴→ finalize
```

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

只执行selected branch；未选candidate仍出现在dynamic semantic projection中，状态为`skipped`，reason code为`route-not-selected`。

## 业务代码

- `definition.go`：recorded route、selected/skipped节点和共同finalize；
- `activities.go`：route、concise、detailed与finalize业务逻辑。

Workflow不能直接访问外部服务。它只依据Temporal已记录的Activity result作deterministic决策。

## 测试

```sh
make test
make vet
make verify
```

测试分别覆盖`concise`与`detailed`，并证明未选Activity handler不会执行。

## Build、push或kind-load

```sh
make image VERSION=2026.08.1 COMMIT=$(git rev-parse --short=12 HEAD)
```

push自己的registry：

```sh
make push \
  IMAGE_REPOSITORY=registry.example.com/team/dynamic-decision-worker \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

或加载本地`kind-org`：

```sh
make kind-load VERSION=2026.08.1 COMMIT=$(git rev-parse --short=12 HEAD)
```

成功后复制`IMAGE_DIGEST=<repository>@sha256:...`。构建只使用当前repository；根级generator与org私有源码不是前置条件。

## 在org中运行

1. 在Console创建Worker `dynamic-decision-worker`；
2. 用digest发布Version，并等待SDK registration、poller与probe；
3. 触发`DynamicDecisionWorkflow`，input为`{"mode":"concise","subject":"release notes"}`；
4. 观察`concise-branch=completed`、`detailed-branch=skipped`与`finalize=completed`；
5. 再用`mode=detailed`触发独立Run，观察相反路径。

Org SDK从typed Definition生成contract并在启动时自动注册。Console只读展示contract和动态DAG，不从Temporal Event History猜路径。

## 哪些配置由平台注入

org平台注入bootstrap endpoint/token、Pod identity、Temporal连接、Task Queue、Worker Deployment和Build ID。用户不手填这些值，也不把credential或routing写进image。

用户只维护业务Definition/Activities、image与release输入。发布body示例见`config/release.example.json`。Secret或敏感input不得进入projection、log或Audit。
