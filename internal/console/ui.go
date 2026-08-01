package console

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/*
var webFiles embed.FS

var consoleTemplate = template.Must(template.ParseFS(webFiles, "web/index.html"))

type consolePage struct {
	Page        string
	Title       string
	Description string
	Identity    Identity
	Breadcrumb  []breadcrumbItem
}

type breadcrumbItem struct {
	Label   string
	Href    string
	Current bool
}

func consoleRoute(path string) (consolePage, bool) {
	parts := pathParts(path)
	switch {
	case path == "/":
		return consolePage{Page: "overview", Title: "总览", Description: "查看当前 Tenant 的运行概况与资源使用。", Breadcrumb: []breadcrumbItem{{Label: "总览", Current: true}}}, true
	case path == "/tenants":
		return consolePage{Page: "tenants", Title: "管理 Tenants", Description: "查看你有权限访问的 Tenant、quota 与成员。", Breadcrumb: []breadcrumbItem{{Label: "管理 Tenants", Current: true}}}, true
	case len(parts) == 2 && parts[0] == "tenants" && parts[1] != "":
		return consolePage{Page: "tenant", Title: parts[1], Description: "管理该 Tenant 的显示信息、quota policy 与既有 platform principal membership。", Breadcrumb: []breadcrumbItem{{Label: "管理 Tenants", Href: "/tenants"}, {Label: parts[1], Current: true}}}, true
	case path == "/workers":
		return consolePage{Page: "workers", Title: "Workers", Description: "管理当前 Tenant 的逻辑 Worker 及其独立版本。", Breadcrumb: []breadcrumbItem{{Label: "Workers", Current: true}}}, true
	case len(parts) == 2 && parts[0] == "workers" && parts[1] != "":
		return consolePage{Page: "worker", Title: parts[1], Description: "查看当前 Tenant 中该 Worker 的 Current 版本、历史版本与最近 Runs。", Breadcrumb: []breadcrumbItem{{Label: "Workers", Href: "/workers"}, {Label: parts[1], Current: true}}}, true
	case len(parts) == 4 && parts[0] == "workers" && parts[1] != "" && parts[2] == "versions" && parts[3] != "":
		return consolePage{Page: "version", Title: parts[1] + " · " + parts[3], Description: "查看当前 Tenant 中的 release、部署健康与经验证的只读 contract。", Breadcrumb: []breadcrumbItem{{Label: "Workers", Href: "/workers"}, {Label: parts[1], Href: "/workers/" + parts[1]}, {Label: "Versions"}, {Label: parts[3], Current: true}}}, true
	case path == "/workflows":
		return consolePage{Page: "workflows", Title: "Workflows", Description: "在当前 Tenant 下按 Worker 与版本浏览并启动 Workflow。", Breadcrumb: []breadcrumbItem{{Label: "Workflows", Current: true}}}, true
	case len(parts) == 6 && parts[0] == "workers" && parts[1] != "" && parts[2] == "versions" && parts[3] != "" && parts[4] == "workflows" && parts[5] != "":
		return consolePage{Page: "workflow", Title: parts[5], Description: "在当前 Tenant 下检查只读输入 contract，选择版本并启动独立 Run。", Breadcrumb: []breadcrumbItem{{Label: "Workers", Href: "/workers"}, {Label: parts[1], Href: "/workers/" + parts[1]}, {Label: "Versions"}, {Label: parts[3], Href: "/workers/" + parts[1] + "/versions/" + parts[3]}, {Label: "Workflows", Href: "/workflows"}, {Label: parts[5], Current: true}}}, true
	case path == "/runs":
		return consolePage{Page: "runs", Title: "Runs", Description: "查看当前 Tenant 的 Workflow Run 与所选 WorkerVersion。", Breadcrumb: []breadcrumbItem{{Label: "Runs", Current: true}}}, true
	case len(parts) == 2 && parts[0] == "runs" && parts[1] != "":
		return consolePage{Page: "run", Title: parts[1], Description: "基于当前 Tenant Worker 的 semantic projection 查看运行时 DAG。", Breadcrumb: []breadcrumbItem{{Label: "Runs", Href: "/runs"}, {Label: parts[1], Current: true}}}, true
	default:
		return consolePage{}, false
	}
}

func (s *server) serveConsole(response http.ResponseWriter, requestID string, identity Identity, page consolePage) {
	page.Identity = identity
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_ = consoleTemplate.Execute(response, page)
}

func serveAsset(response http.ResponseWriter, request *http.Request) bool {
	if request.Method != http.MethodGet || !strings.HasPrefix(request.URL.Path, "/assets/") {
		return false
	}
	name := strings.TrimPrefix(request.URL.Path, "/assets/")
	if name != "app.css" && name != "yaml-renderer.js" && name != "app.js" {
		return false
	}
	contents, err := fs.ReadFile(webFiles, "web/"+name)
	if err != nil {
		return false
	}
	if strings.HasSuffix(name, ".css") {
		response.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else {
		response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	response.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = response.Write(contents)
	return true
}
