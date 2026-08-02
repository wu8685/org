# 023 org 品牌标志接入

**状态：Approved**
**日期：2026-08-02**

## 目标

将 org Logo V2 接入项目文档、Console 产品导航和浏览器 tab。标志用于建立产品身份，不改变 Console 现有的信息架构、业务流程或黑白工业感视觉方向。

品牌语义保持一致：开放圆环表示 Tenant / Worker 的运行边界，cobalt 路径表示一次从输入穿越边界、抵达结果的执行。

## 权威资产

仓库保存以下 4 个原始 SVG，文件内容来自 `org-logo-v2-assets.zip`，不得通过截图重绘或栅格化：

| 文件 | 用途 |
|---|---|
| `org-logo-v2.svg` | 浅色产品界面的彩色横向组合标志 |
| `org-logo-mark-v2.svg` | 浅色紧凑界面的彩色图形标志 |
| `org-logo-mono-v2.svg` | GitHub README 等文档入口 |
| `org-logo-favicon-v2.svg` | 浏览器 favicon；自带深色圆角容器 |

资产在仓库中只保留一份权威副本。Go binary 从该目录 embed 并通过 Console 的 `/assets/` 路由提供，不在 UI 目录复制第二份。

## Console

- 宽侧栏使用 `org-logo-v2.svg`，旁边保留 `Console` 产品面标识；组合整体链接到 `/`，accessible name 为 `org Console 首页`。
- 介于 desktop 与 mobile 之间的紧凑侧栏使用 `org-logo-mark-v2.svg`，不得把横标压缩到不可读尺寸。
- mobile bottom navigation 沿用现有结构，不额外挤入标志。
- Logo 使用固定高度和 `width: auto`，不得拉伸、裁切、着色或加阴影；四周保留至少一个蓝色端点方块宽度的净空。
- 图片加载失败时，链接仍保留可访问名称；不显示重复的 `org` 文字占位。
- 页面继续使用现有 `--accent: #2f6feb`，不因接入 Logo 扩大品牌色面积。

## 浏览器 tab

- 所有 Console HTML 页面在 `<head>` 中声明 `/assets/org-logo-favicon-v2.svg`，类型为 `image/svg+xml`。
- favicon 路由返回 `image/svg+xml` 和与现有静态资源一致的有界 public cache header。
- 页面标题继续为 `{页面标题} · org Console`；Logo 不替代文本标题。

## 文档

- 根 `README.md` 顶部使用单色横标，视觉优先级低于项目价值说明，不增加品牌口号或技术选型叙事。
- `docs/README.md` 作为文档入口使用较小的单色横标；子文档不逐页重复 Logo，避免阅读噪音。
- Markdown 中使用仓库相对路径和有意义的 alt text；不引用 `~/Downloads`、绝对本地路径或外部 CDN。

## 静态资源与安全

- 静态资源路由采用明确 allowlist，只暴露上述 4 个 SVG；任意其他文件名继续返回 not found。
- SVG 作为不可变的仓库资产原样提供，不接受用户上传内容，不在 DOM 中以内联 HTML 注入。
- 响应设置正确的 MIME type；Go embed 保持单一 binary 交付。

## 验收

1. Console shell 包含 favicon、宽侧栏横标和紧凑侧栏图形标，均有正确语义。
2. 4 个 SVG 都可从 `/assets/{filename}` 读取，Content-Type 为 `image/svg+xml`，未知 SVG 仍为 404。
3. desktop、紧凑侧栏和 mobile 三个断点下，Logo 不变形、不遮挡导航，mobile 不增加新的底栏项目。
4. 根 README 与文档首页能在 GitHub Markdown 中显示单色标志，且不依赖本地文件。
5. `go test ./internal/console`、文档链接测试与项目格式检查通过。
