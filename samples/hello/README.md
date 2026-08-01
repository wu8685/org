# Hello Org SDK Worker

这是最小的Org SDK Worker repository。它用两个无外部副作用的Activity生成问候语，并把顺序执行路径投影给org：

```text
prepare-greeting → compose-greeting → completed
```

> 产品术语遵循 [org glossary](https://github.com/wu8685/org/blob/main/docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 先看两处

- `definition.go`：typed Definition、节点依赖与retry/timeout policy；
- `activities.go`：业务输入校验和问候语生成。

Sample不import raw Temporal SDK，不手写projection或平台routing。Org SDK负责stable node/Activity ID、dynamic semantic projection和启动时contract registration。

## 测试

从本目录运行：

```sh
make test
make vet
# 或一次执行
make verify
```

预期结果：输入`{"name":"Codex"}`后返回`Hello, Codex!`，projection中的三个节点均为`completed`。

## 构建本地image

```sh
make image \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

Docker build context只有当前repository。命令输出可读tag；发布WorkerVersion仍必须使用immutable digest。

## Push到registry

先用Docker完成registry login，再运行：

```sh
make push \
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

成功后输出：

```text
IMAGE_DIGEST=registry.example.com/team/hello-worker@sha256:<digest>
```

脚本不保存registry credential。不要从tag文本推测digest，始终使用registry返回值。

## 本地kind

已有`kind-org`时：

```sh
make kind-load \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

它会构建、加载image并输出`IMAGE_DIGEST=org.local/hello-worker@sha256:...`。

## 在org中运行

1. 在Console创建Worker `hello-worker`；
2. 新建Version，填写version-level description、`IMAGE_DIGEST`、`100m` CPU、`128Mi` memory和source provenance；
3. 等待候选Worker完成SDK registration、poller与probe；
4. 触发`HelloWorkflow`，input为`{"name":"Codex"}`；
5. 在Run detail观察顺序DAG与最终结果。

Org SDK在Worker启动时从typed Definition生成contract并自动注册。用户不管理contract artifact，Console只读展示注册结果。

## 哪些配置由平台注入

部署时org平台注入bootstrap endpoint/token、Pod identity、Temporal连接、Task Queue、Worker Deployment和Build ID。它们不是用户填写的`.env`配置，也不得打进image或提交到Git。

用户只维护业务Definition/Activities、image repository、release description、resource配置、Secret reference和source provenance。示例发布body见`config/release.example.json`。

真实write Activity必须使用stable idempotency key，或声明reconciliation/compensation policy；平台不声称外部效果exactly once。
