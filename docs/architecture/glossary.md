# org 术语表

本术语表是 `org` 产品、API、UI、规格与开发文档的规范引用。若其他文档中的术语与本表冲突，以本表为准。

## 强制术语

| 概念 | 规范名称 | 禁止或受限用法 |
|---|---|---|
| `org` 中的用户、数据与授权边界 | **Tenant** | 新文档不得把它简称为 Namespace 或“命名空间” |
| Temporal 的底层共享资源边界 | **platform Temporal Namespace** | 不得简称为 Temporal Namespace、shared Namespace 或 Tenant Namespace |
| Kubernetes 的底层共享 workload 边界 | **platform Kubernetes Namespace** | 不得简称为 Kubernetes Namespace、workload Namespace 或 Tenant Namespace |

产品/API/UI 层统一使用 **Tenant**。如果未来 UI 因迁移兼容必须显示 `Namespace`，唯一允许的显示形式是 **Namespace (Tenant)**，并且 API/domain contract 仍使用 `tenant` / `tenantId`，不得重新引入产品级 `namespace` 字段。

技术文档、配置说明和诊断信息必须完整写出 **platform Temporal Namespace** 或 **platform Kubernetes Namespace**。配置键、Kubernetes CLI resource name、Temporal SDK 字段及 containerd 的 `--namespace k8s.io` 保持各自真实技术拼写；这些实现名不构成产品术语。

## 当前资源关系

当前部署模型固定为：

```text
many Tenants
  -> one shared platform Temporal Namespace
  -> one shared platform Kubernetes Namespace
```

- 平台当前只有一个共享 platform Temporal Namespace；所有 Tenant 的 Task Queue、Worker Deployment 与 Workflow ID 由服务端生成的 tenant-qualified name 区分。
- 平台当前只有一个共享 platform Kubernetes Namespace；所有 Tenant 的 workload 由 tenant-qualified resource name、label、ServiceAccount、安全默认和 `org` quota 隔离。
- Tenant 不映射为、也不拥有底层 platform Temporal Namespace 或 platform Kubernetes Namespace。
- 共享底层资源不是硬多租户隔离。需要更强隔离时必须另写架构规格，不能通过把 Tenant 改称 Namespace 暗示隔离已经存在。

## 使用示例

正确：

- “从认证主体派生 Tenant。”
- “Worker 部署到共享 platform Kubernetes Namespace。”
- “Worker 连接共享 platform Temporal Namespace。”
- “UI 显示 Namespace (Tenant)”（仅迁移兼容场景）。

错误：

- “创建用户 Namespace。”
- “每个 Tenant 对应一个 Kubernetes/Temporal Namespace。”
- “从请求 body 读取 namespace 选择 Tenant。”
- “共享 Namespace 提供 Tenant 级硬隔离。”
