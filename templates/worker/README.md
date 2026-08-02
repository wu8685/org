# Org Worker Starter

这是创建 Org Worker 的起点。

```bash
go mod edit -module example.com/acme/my-worker
make verify
```

修改 module path 后，同时更新 `cmd/worker/main.go` 中的 Worker package import。

接着按照[创建你的第一个 Worker](https://github.com/wu8685/org/blob/main/docs/user/create-your-worker.md)修改业务代码、构建 image 并发布。
