# Org SDK Samples

三个Sample是一条由浅入深的学习路径。每个目录都按独立Worker repository组织：进入目录后即可测试、构建、push或加载kind，不依赖org根Makefile。

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

| 顺序 | Sample | 学习目标 | Console中可观察结果 |
|---|---|---|---|
| 1 | [Hello](hello/README.md) | 两个顺序Activity与最小typed Definition | `prepare → compose → completed` |
| 2 | [Parallel confirmation](parallel-confirmation/README.md) | `waiting-for-user`、受控确认、runtime fork/join | 确认后两个并行节点同时运行并汇合 |
| 3 | [Dynamic decision](dynamic-decision/README.md) | Activity result驱动if/else | selected分支完成，未选分支显示`skipped` |

## 每个目录使用相同命令

```sh
cd samples/hello # 或另一个Sample目录
make test
make verify
SOURCE_REVISION=abcdef1 # 替换为你的7–64位hexadecimal source revision
make kind-load VERSION=2026.08.1 COMMIT="$SOURCE_REVISION"
```

使用自己的registry时：

```sh
SOURCE_REVISION=abcdef1 # 替换为你的7–64位hexadecimal source revision
make push \
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT="$SOURCE_REVISION"
```

两条image路径都输出`IMAGE_DIGEST=<repository>@sha256:...`。在org Console创建对应Worker并发布Version，候选Pod会通过Org SDK自动注册contract；用户不提供contract文件。

Version发布字段和digest-only请求契约见 [Publish a WorkerVersion](https://github.com/wu8685/org/blob/main/docs/api/publish-worker-version.md)。Sample repository不保存控制面请求body。

平台注入bootstrap、Temporal连接和routing配置。Sample repository只拥有业务Definition、Activities、image构建与release输入。不要把Secret写进image、Workflow input、projection、log或Audit。

完整本地环境与发布步骤见 [Getting Started](../docs/getting-started.md)。
