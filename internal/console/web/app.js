(() => {
  "use strict";

  const body = document.body;
  const page = body.dataset.page;
  const content = document.querySelector("[data-page-content]");
  const notice = document.querySelector("[data-notice]");
  const actions = document.querySelector("[data-heading-actions]");
  const csrf = document.querySelector('meta[name="csrf-token"]').content;
  const payloadCodec = window.OrgYAML;
  const yamlRenderer = payloadCodec;
  const segments = location.pathname.split("/").filter(Boolean).map(decodeURIComponent);
  let lastRunETag = "";
	let lastRunsETag = "";
	let tenantDetailETag = "";
  let actionContext = null;
  let runPoll = 0;
	let runsPoll = 0;

  const tenantSwitch = document.querySelector("[data-tenant-switch]");
  const tenantSwitchStatus = document.querySelector("[data-tenant-switch-status]");
  tenantSwitch?.elements.tenantSlug.addEventListener("change", async event => {
    const select = event.target;
    const previous = tenantSwitch.dataset.currentTenant;
    select.disabled = true;
    tenantSwitchStatus.textContent = "正在切换 Tenant…";
    try {
      const result = await api("/api/v1/session/tenant", {method: "POST", body: JSON.stringify({tenantSlug: select.value})});
      tenantSwitchStatus.textContent = "Tenant 已切换，正在刷新…";
      location.href = result.redirect || "/";
    } catch (error) {
      select.value = previous;
      select.disabled = false;
      tenantSwitchStatus.textContent = error.message || "Tenant 切换失败。";
    }
  });

  document.querySelectorAll("[data-nav]").forEach(link => {
    const section = page === "worker" || page === "version" ? "workers" : page === "workflow" ? "workflows" : page === "run" ? "runs" : page;
    if (link.dataset.nav === section) link.setAttribute("aria-current", "page");
  });

  const el = (tag, attrs = {}, children = []) => {
    const node = document.createElement(tag);
    Object.entries(attrs).forEach(([key, value]) => {
      if (key === "class") node.className = value;
      else if (key === "text") node.textContent = value == null ? "—" : String(value);
      else if (key.startsWith("data-")) node.setAttribute(key, value);
      else node[key] = value;
    });
    (Array.isArray(children) ? children : [children]).filter(Boolean).forEach(child => node.append(child));
    return node;
  };

  const api = async (path, options = {}) => {
    const headers = new Headers(options.headers || {});
    headers.set("Accept", "application/json");
    if (options.body) headers.set("Content-Type", "application/json");
    if (options.method && options.method !== "GET") headers.set("X-CSRF-Token", csrf);
    const response = await fetch(path, {...options, headers});
    if (response.status === 304) return {notModified: true, etag: response.headers.get("ETag")};
    const value = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(value.error?.message || `Request failed (${response.status})`);
      error.code = value.error?.code;
      error.status = response.status;
      throw error;
    }
    return {...value, etag: response.headers.get("ETag"), location: response.headers.get("Location")};
  };

  const clear = target => { while (target.firstChild) target.firstChild.remove(); };
  const showNotice = (message, persistent = false) => {
    notice.textContent = message;
    notice.hidden = !message;
    if (message && !persistent) setTimeout(() => { if (notice.textContent === message) notice.hidden = true; }, 5000);
  };
  const status = (value, appearance = value) => el("span", {class: `status status-${String(appearance || "pending").toLowerCase()}`, text: value || "unknown"});
  const mono = value => el("span", {class: "mono", text: value});
  const card = children => el("article", {class: "card"}, children);
  const metric = (label, value, note) => card([
    el("span", {class: "metric-label", text: label}), el("strong", {class: "metric-value", text: value}), el("span", {class: "metric-note", text: note}),
  ]);
  const link = (text, href) => el("a", {text, href});
  const button = (text, handler, secondary = false) => {
    const result = el("button", {type: "button", class: `btn ${secondary ? "btn-secondary" : "btn-primary"}`, text});
    result.addEventListener("click", handler);
    return result;
  };
  const section = (title, child) => el("section", {class: "section-block"}, [el("div", {class: "section-head"}, el("h2", {text: title})), child]);
  const table = (headers, rows) => {
    const head = el("thead", {}, el("tr", {}, headers.map(item => el("th", {scope: "col", text: item}))));
    const tbody = el("tbody");
    rows.forEach(cells => tbody.append(el("tr", {}, cells.map((cell, index) => {
      const td = el("td", {"data-label": headers[index]});
      td.append(cell instanceof Node ? cell : document.createTextNode(String(cell ?? "—")));
      return td;
    }))));
    return el("div", {class: "table-wrap"}, el("table", {}, [head, tbody]));
  };
  const empty = message => el("div", {class: "card empty", text: message});
  const definition = entries => {
    const list = el("dl", {class: "definition"});
    entries.forEach(([key, value]) => list.append(el("dt", {text: key}), value instanceof Node ? el("dd", {}, value) : el("dd", {text: value})));
    return list;
  };

  function yamlView(value, label) {
    const rendered = yamlRenderer.render(value);
    const view = el("div", {class: "yaml-view", "data-yaml-view": ""});
    const toolbar = el("div", {class: "yaml-toolbar"});
    const format = el("span", {class: "yaml-format", text: "YAML"});
    const copyStatus = el("span", {class: "copy-status"});
    copyStatus.setAttribute("aria-live", "polite");
    const copy = button("复制 YAML", async () => {
      try {
        if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
        await navigator.clipboard.writeText(rendered.text);
        copyStatus.textContent = "已复制";
      } catch (_) {
        copyStatus.textContent = "复制失败，请手动选择";
      }
    }, true);
    copy.setAttribute("aria-label", `复制 ${label} YAML`);
    if (!rendered.ok) {
      copy.disabled = true;
      copyStatus.textContent = "无法安全显示；请查看 API 原始 JSON";
    }
    const code = el("pre", {class: "contract-view yaml-code", text: rendered.text, tabindex: 0});
    code.setAttribute("aria-label", `${label} YAML`);
    toolbar.append(format, copyStatus, copy);
    view.append(toolbar, code);
    return view;
  }

  function buildSchemaFields(schema, fieldset, jsonTextarea) {
    clear(fieldset);
    const properties = schema?.properties || {};
    const required = new Set(schema?.required || []);
    const updateJSON = () => {
      const value = {};
      fieldset.querySelectorAll("[data-schema-property]").forEach(input => {
        if (input.type === "checkbox") value[input.dataset.schemaProperty] = input.checked;
        else if (input.value !== "") value[input.dataset.schemaProperty] = input.type === "number" ? Number(input.value) : input.value;
      });
      jsonTextarea.value = JSON.stringify(value, null, 2);
    };
    Object.entries(properties).forEach(([name, property]) => {
      const type = property.type === "boolean" ? "checkbox" : ["number", "integer"].includes(property.type) ? "number" : "text";
      const input = el("input", {type, required: required.has(name)});
      input.dataset.schemaProperty = name;
      if (property.description) input.setAttribute("aria-description", property.description);
      input.addEventListener("input", updateJSON);
      fieldset.append(el("label", {text: `${property.title || name}${required.has(name) ? " *" : ""}`}, input));
    });
    if (!Object.keys(properties).length) fieldset.append(el("p", {class: "muted", text: "此操作无需输入字段。"}));
    updateJSON();
  }

  function exampleFromSchema(schema) {
    if (!schema || typeof schema !== "object") return null;
    if (Object.prototype.hasOwnProperty.call(schema, "default")) return schema.default;
    if (Object.prototype.hasOwnProperty.call(schema, "example")) return schema.example;
    if (Array.isArray(schema.enum) && schema.enum.length) return schema.enum[0];
    switch (schema.type) {
    case "object": {
      const value = {};
      const required = new Set(schema.required || []);
      Object.entries(schema.properties || {}).forEach(([name, child]) => {
        if (required.has(name)) value[name] = exampleFromSchema(child);
      });
      return value;
    }
    case "array": return [];
    case "boolean": return false;
    case "number": case "integer": return 0;
    case "string": return "";
    default: return null;
    }
  }

  async function renderOverview() {
    const {overview} = await api("/api/v1/overview");
    clear(content);
    content.append(el("div", {class: "grid-4"}, [
      metric("Workers", overview.workers.total, "逻辑 Worker"),
      metric("Ready versions", overview.versions.ready, `${overview.versions.failed} failed`),
      metric("Runs", overview.runs.total, "已登记 Run"),
      metric("Concurrent runs", overview.quotaUsage.concurrentRuns, `上限 ${overview.quotaPolicy.maxConcurrentRuns}`),
    ]));
    content.append(section("Tenant quota", card(definition([
	  ["Tenant context", `${overview.tenantId} · ${overview.tenantStatus}`],
      ["Active releases", `${overview.quotaUsage.activeReleases} / ${overview.quotaPolicy.maxActiveReleases}`],
      ["Worker Pods", `${overview.quotaUsage.activeWorkerPods} / ${overview.quotaPolicy.maxActiveWorkerPods}`],
      ["Reserved CPU", `${overview.quotaUsage.reservedCpuMilli}m / ${overview.quotaPolicy.maxReservedCPU}`],
      ["Reserved memory", `${overview.quotaUsage.reservedMemoryBytes} bytes / ${overview.quotaPolicy.maxReservedMemory}`],
    ]))));
  }

	const tenantDialog = document.querySelector("[data-tenant-dialog]");
	const tenantForm = document.querySelector("[data-tenant-form]");
	const tenantError = document.querySelector("[data-tenant-error]");
	const tenantQuotaFields = document.querySelector("[data-tenant-quota-fields]");
	let tenantDialogMode = "create";
	let tenantDialogSlug = "";
	let tenantDialogTrigger = null;
	function showFormError(target, error) {
		target.textContent = error?.message || "请求失败。";
		target.hidden = false;
	}
	function openTenantDialog(view = null) {
		tenantDialogTrigger = document.activeElement;
		tenantDialogMode = view ? "update" : "create";
		tenantDialogSlug = view?.tenant.slug || "";
		tenantForm.reset(); tenantError.hidden = true;
		tenantForm.elements.slug.disabled = Boolean(view);
		tenantQuotaFields.hidden = !view;
		tenantQuotaFields.disabled = !view;
		document.querySelector("[data-tenant-dialog-title]").textContent = view ? `更新 ${view.tenant.displayName}` : "创建 Tenant";
		document.querySelector("[data-tenant-submit]").textContent = view ? "保存 Tenant" : "创建 Tenant";
		if (view) {
			tenantForm.elements.slug.value = view.tenant.slug;
			tenantForm.elements.displayName.value = view.tenant.displayName;
			tenantForm.elements.description.value = view.tenant.description || "";
			Object.entries(view.tenant.quotaPolicy).forEach(([key, value]) => { if (tenantForm.elements[key]) tenantForm.elements[key].value = value; });
		}
		tenantDialog.showModal();
		(view ? tenantForm.elements.displayName : tenantForm.elements.slug).focus();
	}
	tenantDialog.addEventListener("close", () => tenantDialogTrigger?.focus());
	tenantForm.addEventListener("submit", async event => {
		event.preventDefault(); tenantError.hidden = true;
		if (!tenantForm.reportValidity()) return;
		try {
			if (tenantDialogMode === "create") {
				const result = await api("/api/v1/tenants", {method: "POST", body: JSON.stringify({slug: tenantForm.elements.slug.value, displayName: tenantForm.elements.displayName.value, description: tenantForm.elements.description.value})});
				tenantDialog.close(); location.href = result.redirect || `/tenants/${encodeURIComponent(result.tenant.tenant.slug)}`;
				return;
			}
			const quotaPolicy = {
				maxReservedCPU: tenantForm.elements.maxReservedCPU.value, maxReservedMemory: tenantForm.elements.maxReservedMemory.value,
				maxActiveWorkerPods: Number(tenantForm.elements.maxActiveWorkerPods.value), maxActiveReleases: Number(tenantForm.elements.maxActiveReleases.value),
				maxConcurrentRuns: Number(tenantForm.elements.maxConcurrentRuns.value), maxConcurrentDeployments: Number(tenantForm.elements.maxConcurrentDeployments.value),
			};
			await api(`/api/v1/tenants/${encodeURIComponent(tenantDialogSlug)}`, {method: "PATCH", headers: {"If-Match": tenantDetailETag}, body: JSON.stringify({displayName: tenantForm.elements.displayName.value, description: tenantForm.elements.description.value, quotaPolicy})});
			tenantDialog.close(); location.reload();
		} catch (error) { showFormError(tenantError, error); }
	});

	const memberDialog = document.querySelector("[data-member-dialog]");
	const memberForm = document.querySelector("[data-member-form]");
	const memberError = document.querySelector("[data-member-error]");
	let memberDialogMode = "add";
	let memberDialogTenant = "";
	let memberDialogRevision = 0;
	let memberDialogTrigger = null;
	function openMemberDialog(tenantSlug, member = null, mode = "add") {
		memberDialogTrigger = document.activeElement;
		memberDialogMode = mode; memberDialogTenant = tenantSlug; memberDialogRevision = member?.revision || 0;
		memberForm.reset(); memberError.hidden = true;
		memberForm.elements.principalId.disabled = mode !== "add";
		memberForm.elements.role.disabled = mode === "remove";
		if (member) { memberForm.elements.principalId.value = member.principalId; memberForm.elements.role.value = member.role; }
		document.querySelector("[data-member-remove-warning]").hidden = mode !== "remove";
		document.querySelector("[data-member-dialog-title]").textContent = mode === "add" ? "添加 Tenant 成员" : mode === "remove" ? "移除 Tenant 成员" : "更新 Tenant role";
		document.querySelector("[data-member-submit]").textContent = mode === "add" ? "添加成员" : mode === "remove" ? "移除成员" : "保存 role";
		memberDialog.showModal();
		(mode === "add" ? memberForm.elements.principalId : memberForm.elements.role).focus();
	}
	memberDialog.addEventListener("close", () => memberDialogTrigger?.focus());
	memberForm.addEventListener("submit", async event => {
		event.preventDefault(); memberError.hidden = true;
		try {
			const principalID = memberForm.elements.principalId.value;
			if (memberDialogMode === "add") {
				await api(`/api/v1/tenants/${encodeURIComponent(memberDialogTenant)}/members`, {method: "POST", body: JSON.stringify({principalId: principalID, role: memberForm.elements.role.value})});
			} else if (memberDialogMode === "remove") {
				await api(`/api/v1/tenants/${encodeURIComponent(memberDialogTenant)}/members/${encodeURIComponent(principalID)}`, {method: "DELETE", headers: {"If-Match": `\"member-r${memberDialogRevision}\"`}});
				memberDialog.close(); location.href = "/tenants"; return;
			} else {
				await api(`/api/v1/tenants/${encodeURIComponent(memberDialogTenant)}/members/${encodeURIComponent(principalID)}`, {method: "PATCH", headers: {"If-Match": `\"member-r${memberDialogRevision}\"`}, body: JSON.stringify({role: memberForm.elements.role.value})});
			}
			memberDialog.close(); await renderTenant();
		} catch (error) { showFormError(memberError, error); }
	});

	async function renderTenants() {
		const {items} = await api("/api/v1/tenants");
		clear(content); clear(actions);
		if (items.some(item => item.allowedActions?.create)) actions.append(button("创建 Tenant", () => openTenantDialog()));
		if (!items.length) return content.append(empty("当前 principal 没有可管理的 Tenant。"));
		content.append(el("div", {class: "tenant-grid"}, items.map(view => el("article", {class: "card tenant-card"}, [
			el("p", {class: "eyebrow", text: `Tenant · ${view.membership.role}`}), el("h2", {}, link(view.tenant.displayName, `/tenants/${encodeURIComponent(view.tenant.slug)}`)),
			mono(view.tenant.slug), status(view.tenant.status), el("p", {class: "tenant-description", text: view.tenant.description || "暂无说明"}),
			el("p", {class: "muted", text: `Runs ${view.quotaUsage.concurrentRuns} / ${view.tenant.quotaPolicy.maxConcurrentRuns}`}),
		]))));
	}

	async function renderTenant() {
		const slug = segments[1];
		const response = await api(`/api/v1/tenants/${encodeURIComponent(slug)}`);
		const view = response.tenant;
		tenantDetailETag = response.etag || "";
		clear(content); clear(actions);
		if (view.allowedActions?.update) actions.append(button("更新 Tenant", () => openTenantDialog(view), true));
		if (view.allowedActions?.manageMembers && view.permissions.includes("tenant:member:manage")) actions.append(button("添加成员", () => openMemberDialog(slug)));
		content.append(el("div", {class: "grid-2"}, [
			card([el("h2", {text: "Tenant"}), definition([["Display name", view.tenant.displayName], ["Stable slug", mono(view.tenant.slug)], ["Stable identifier", mono(view.tenant.id)], ["Status", status(view.tenant.status)], ["Description", view.tenant.description || "—"], ["Your role", view.membership.role]])]),
			card([el("h2", {text: "Quota"}), definition([["Concurrent Runs", `${view.quotaUsage.concurrentRuns} / ${view.tenant.quotaPolicy.maxConcurrentRuns}`], ["Active releases", `${view.quotaUsage.activeReleases} / ${view.tenant.quotaPolicy.maxActiveReleases}`], ["Worker Pods", `${view.quotaUsage.activeWorkerPods} / ${view.tenant.quotaPolicy.maxActiveWorkerPods}`], ["Reserved CPU", `${view.quotaUsage.reservedCpuMilli}m / ${view.tenant.quotaPolicy.maxReservedCPU}`], ["Reserved memory", `${view.quotaUsage.reservedMemoryBytes} bytes / ${view.tenant.quotaPolicy.maxReservedMemory}`]])]),
		]));
		const canManage = view.allowedActions?.manageMembers;
		content.append(section("Members", view.members?.length ? table(["Principal", "Role", "Permissions", "Actions"], view.members.map(member => {
			const memberActions = el("div", {class: "member-actions"});
			if (canManage) { memberActions.append(button("更新 role", () => openMemberDialog(slug, member, "edit"), true), button("移除", () => openMemberDialog(slug, member, "remove"), true)); }
			return [el("span", {text: `${member.principalDisplayName} · ${member.principalId}`}), status(member.role), (member.permissions || []).join(", "), memberActions];
		})) : empty("此 Tenant 暂无可见成员。")));
	}

  async function renderWorkers() {
    const {items} = await api("/api/v1/workers");
    clear(content); clear(actions);
    actions.append(button("创建 Worker", openWorkerDialog));
    if (!items.length) return content.append(empty("当前 Tenant 还没有 Worker。"));
    content.append(table(["Worker", "Current", "Created"], items.map(worker => [
      link(worker.workerName || worker.name, `/workers/${encodeURIComponent(worker.workerName || worker.name)}`),
      mono(worker.currentVersion || "尚未发布"),
      new Date(worker.createdAt).toLocaleString(),
    ])));
  }

  const workerDialog = document.querySelector("[data-worker-dialog]");
  const workerForm = document.querySelector("[data-worker-form]");
  const workerName = workerForm.elements.workerName;
  const workerError = document.querySelector("[data-worker-error]");
  let workerDialogTrigger = null;
  function clearWorkerError() {
    workerError.textContent = "";
    workerError.hidden = true;
    workerName.removeAttribute("aria-invalid");
  }
  function openWorkerDialog() {
    workerDialogTrigger = document.activeElement;
    workerForm.reset();
    clearWorkerError();
    workerDialog.showModal();
    workerName.focus();
  }
  workerName.addEventListener("input", clearWorkerError);
  workerDialog.addEventListener("close", () => workerDialogTrigger?.focus());
  workerForm.addEventListener("submit", async event => {
    event.preventDefault();
    if (!workerForm.reportValidity()) return;
    try {
      const name = workerName.value;
      await api("/api/v1/workers", {method: "POST", body: JSON.stringify({workerName: name})});
      workerDialog.close();
      location.href = `/workers/${encodeURIComponent(name)}`;
    } catch (error) {
      workerError.textContent = error.message || "Worker 创建失败。";
      workerError.hidden = false;
      workerName.setAttribute("aria-invalid", "true");
      workerName.focus();
    }
  });

  async function renderWorker() {
    const workerName = segments[1];
    const response = await api(`/api/v1/workers/${encodeURIComponent(workerName)}`);
    clear(content); clear(actions);
    actions.append(button("录入版本", () => openPublish(workerName)));
    content.append(card(definition([
      ["Worker name", mono(response.worker.workerName || response.worker.name)],
      ["Current version", mono(response.worker.currentVersion || "尚未设置")],
    ])));
    const versions = response.versions || [];
    content.append(section("WorkerVersions", versions.length ? table(["Version", "Description", "Health", "Current"], versions.map(version => [
      link(version.version, `/workers/${encodeURIComponent(workerName)}/versions/${encodeURIComponent(version.version)}`),
      version.description,
      status(version.state),
      version.current ? "Current" : "Historical",
    ])) : empty("当前 Tenant 还没有 WorkerVersion。")));
  }

  async function renderVersion() {
    const workerName = segments[1], versionName = segments[3];
    const response = await api(`/api/v1/workers/${encodeURIComponent(workerName)}/versions/${encodeURIComponent(versionName)}`);
    const version = response.workerVersion;
    clear(content); clear(actions);
    actions.append(button("更新 release note", async () => {
      const description = window.prompt("Release description", version.description);
      if (!description || description === version.description) return;
      try {
        await api(`/api/v1/workers/${encodeURIComponent(workerName)}/versions/${encodeURIComponent(versionName)}/description`, {method: "PATCH", headers: {"If-Match": response.etag}, body: JSON.stringify({description})});
        await renderVersion();
      } catch (error) { handleError(error); }
    }, true));
    content.append(el("div", {class: "grid-2"}, [
      card([el("h2", {text: "Release"}), definition([
        ["Worker", workerName], ["Version", mono(version.version)], ["Description", version.description], ["Revision", version.revision], ["Image", mono(version.image)], ["State", status(version.state)],
      ])]),
      card([el("h2", {text: "Contract verification"}), definition([
        ["SDK registration", status(version.registration?.status || "awaiting-registration")], ["Pinned probe", status(version.probe?.status || "pending")], ["Manifest digest", mono(version.contractVerification.manifestDigest || "等待 Worker 注册")], ["SDK", mono(version.contractVerification.sdkModuleVersion || "—")], ["Runtime protocol", mono(version.contractVerification.runtimeProtocolVersion || "—")],
      ])]),
    ]));
    content.append(section("Runtime config", card([definition([["CPU", version.runtime.cpu], ["Memory", version.runtime.memory]]), yamlView(version.versionConfig ?? {}, "Version config")])));
    const contractView = yamlView(version.contract ?? null, "Read-only SDK contract");
    contractView.setAttribute("data-contract-readonly", "");
    content.append(section("Read-only SDK contract", contractView));
  }

  async function renderWorkflows() {
    const {items} = await api("/api/v1/workflows");
    clear(content);
    if (!items.length) return content.append(empty("当前 Tenant 没有 Ready WorkerVersion 提供 Workflow。"));
    content.append(el("div", {class: "grid-2"}, items.map(item => {
      const href = `/workers/${encodeURIComponent(item.workerName)}/versions/${encodeURIComponent(item.workerVersion)}/workflows/${encodeURIComponent(item.workflow.name)}`;
      return card([el("p", {class: "eyebrow", text: `${item.workerName} · ${item.current ? "Current" : "Historical"}`}), el("h2", {}, link(item.workflow.name, href)), el("p", {class: "muted", text: item.versionDescription}), mono(item.workerVersion)]);
    })));
  }

  async function renderWorkflow() {
    const workerName = segments[1], versionName = segments[3], workflowName = segments[5];
    const {workerVersion} = await api(`/api/v1/workers/${encodeURIComponent(workerName)}/versions/${encodeURIComponent(versionName)}`);
    const workflow = (workerVersion.contract.workflows || []).find(item => item.name === workflowName);
    if (!workflow) throw new Error("Workflow contract 不存在");
    clear(content); clear(actions);
    actions.append(button("启动 Run", () => openTrigger(workerName, versionName, workflowName, workflow)));
    content.append(card(definition([["Worker", workerName], ["WorkerVersion", mono(versionName)], ["Release description", workerVersion.description], ["Versioning", workflow.versioningBehavior]])));
    const inputContract = yamlView(workflow.inputSchema ?? {}, "Read-only input contract");
    inputContract.setAttribute("data-contract-readonly", "");
    content.append(section("Read-only input contract", inputContract));
  }

	const runStatusLabels = {running: "Running", "waiting-for-user": "Waiting for user", completed: "Completed", failed: "Failed", canceled: "Cancelled", cancelled: "Cancelled", starting: "Starting", unavailable: "Unavailable"};
	function runStatusSummary(run) {
		const semanticStatus = run.semanticStatus || "unavailable";
		const label = runStatusLabels[semanticStatus] || semanticStatus;
		const wrapper = el("div", {class: "run-status-summary"});
		const badge = status(label, semanticStatus);
		const aria = [`Run status: ${label}`];
		wrapper.append(badge);
		if (run.blockReason) {
			wrapper.append(el("span", {class: "run-block-reason", text: run.blockReason}));
			aria.push(run.blockReason);
		}
		if (semanticStatus === "failed" && run.errorSummary) {
			wrapper.append(el("span", {class: "run-error-summary", text: run.errorSummary.message}));
			aria.push(`Run failed: ${run.errorSummary.message}`);
		}
		wrapper.setAttribute("aria-label", aria.join(". "));
		return wrapper;
	}

	function runFailurePanel(failure) {
		if (!failure) return null;
		return el("section", {class: "run-failure-panel", role: "alert", "data-run-failure": ""}, [
			el("h2", {text: "Run failed"}),
			el("p", {class: "run-failure-message", text: failure.message}),
			definition([
				["Failure code", mono(failure.code)],
				["Failed node", failure.nodeLabel || "—"],
				["Occurred", failure.occurredAt ? new Date(failure.occurredAt).toLocaleString() : "—"],
			]),
		]);
	}

  async function renderRuns(poll = false) {
		const headers = poll && lastRunsETag ? {"If-None-Match": lastRunsETag} : {};
		const response = await api("/api/v1/runs" + location.search, {headers});
		if (response.notModified) return;
		lastRunsETag = response.etag || "";
		const {items} = response;
    clear(content);
    if (!items.length) return content.append(empty("当前 Tenant 的筛选条件下没有 Run。"));
		content.append(table(["Run", "Status", "Current node", "Updated", "Description", "Worker", "Workflow", "Selected version"], items.map(run => [
			link(run.id, `/runs/${encodeURIComponent(run.id)}`), runStatusSummary(run), run.currentNodeSummary || "—", new Date(run.semanticUpdatedAt || run.updatedAt || run.createdAt).toLocaleString(), run.description || "—", run.workerName, run.workflow, mono(run.selectedVersion),
    ])));
		if (!runsPoll) runsPoll = window.setInterval(() => { if (!document.hidden) renderRuns(true).catch(handleError); }, 3000);
  }

  async function renderRun(poll = false) {
    const runID = segments[1];
    const headers = poll && lastRunETag ? {"If-None-Match": lastRunETag} : {};
    const response = await api(`/api/v1/runs/${encodeURIComponent(runID)}`, {headers});
    if (response.notModified) return;
    lastRunETag = response.etag || "";
    const run = response.run, projection = response.semanticProjection;
    clear(content);
    content.append(el("div", {class: "grid-2"}, [
      card([el("h2", {text: "Run"}), definition([["Run ID", mono(run.id)], ["Run description", run.description || "—"], ["Worker", run.workerName], ["Workflow", run.workflow], ["Selected version", mono(run.selectedVersion)], ["Release description", response.workerVersion.description]])]),
      card([el("h2", {text: "Live status"}), definition([["Execution", status(response.execution.status)], ["Projection", projection ? status(projection.runStatus) : "Unavailable"], ["Projection revision", projection?.projectionRevision ?? "—"], ["Allowed actions", projection?.allowedActions?.length || 0]])]),
    ]));
		const failurePanel = runFailurePanel(response.failure);
		if (failurePanel) content.append(failurePanel);
    if (response.temporalDiagnosticsUrl) content.append(section("Advanced diagnostics", card(link("Open advanced diagnostics ↗", response.temporalDiagnosticsUrl))));
    if (!projection) {
      showNotice("Semantic projection 暂不可用；Console 不会猜测业务 DAG。", true);
      document.querySelector("[data-dag-view]").hidden = true;
      return;
    }
    renderDAG(projection, run);
    const active = !["completed", "failed", "canceled", "timed-out"].includes(projection.runStatus);
    if (active && !runPoll) runPoll = window.setInterval(() => { if (!document.hidden) renderRun(true).catch(handleError); }, 2000);
  }

  function renderDAG(semanticProjection, run) {
    const wrapper = document.querySelector("[data-dag-view]");
    const canvas = document.querySelector("[data-dag-canvas]");
    const list = document.querySelector("[data-dag-list]");
    const nodes = semanticProjection.nodes || [];
    wrapper.hidden = false;
    document.querySelector("[data-dag-count]").textContent = `${nodes.length} nodes`;
    clear(canvas); clear(list);

    const byID = new Map(nodes.map(node => [node.runtimeNodeId, node]));
    const memo = new Map();
    const visiting = new Set();
    const layerOf = node => {
      if (memo.has(node.runtimeNodeId)) return memo.get(node.runtimeNodeId);
      if (visiting.has(node.runtimeNodeId)) return 0;
      visiting.add(node.runtimeNodeId);
      const dependencies = (node.dependencies || []).map(id => byID.get(id)).filter(Boolean);
      const layer = dependencies.length ? Math.max(...dependencies.map(layerOf)) + 1 : 0;
      visiting.delete(node.runtimeNodeId); memo.set(node.runtimeNodeId, layer); return layer;
    };
    const layers = new Map();
    nodes.forEach(node => {
      const layer = layerOf(node);
      if (!layers.has(layer)) layers.set(layer, []);
      layers.get(layer).push(node);
    });
    layers.forEach(group => group.sort((a, b) => String(a.createdAt).localeCompare(String(b.createdAt)) || a.runtimeNodeId.localeCompare(b.runtimeNodeId)));
    const maxLayer = Math.max(0, ...layers.keys());
    const maxRows = Math.max(1, ...[...layers.values()].map(group => group.length));
    const width = Math.max(canvas.clientWidth, (maxLayer + 1) * 258 + 50);
    const height = Math.max(420, maxRows * 146 + 50);
    canvas.style.minWidth = `${width}px`; canvas.style.height = `${height}px`;
    const positions = new Map();
    layers.forEach((group, layer) => group.forEach((node, row) => positions.set(node.runtimeNodeId, {x: 28 + layer * 258, y: 28 + row * 146})));

    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "dag-svg"); svg.setAttribute("width", width); svg.setAttribute("height", height); svg.setAttribute("aria-hidden", "true");
    nodes.forEach(node => (node.dependencies || []).forEach(dependencyID => {
      const from = positions.get(dependencyID), to = positions.get(node.runtimeNodeId);
      if (!from || !to) return;
      const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
      const x1 = from.x + 188, y1 = from.y + 54, x2 = to.x, y2 = to.y + 54, midpoint = (x1 + x2) / 2;
      path.setAttribute("class", "dag-edge"); path.setAttribute("d", `M ${x1} ${y1} C ${midpoint} ${y1}, ${midpoint} ${y2}, ${x2} ${y2}`); svg.append(path);
    }));
    canvas.append(svg);

    nodes.forEach(node => {
      const position = positions.get(node.runtimeNodeId);
      const nodeActions = (semanticProjection.allowedActions || []).filter(item => item.runtimeNodeId === node.runtimeNodeId);
      const cardNode = el("article", {class: "dag-node", "data-runtime-node-id": node.runtimeNodeId});
      cardNode.style.left = `${position.x}px`; cardNode.style.top = `${position.y}px`;
      cardNode.dataset.current = String((semanticProjection.currentNodeIds || []).includes(node.runtimeNodeId));
      cardNode.append(status(node.status), el("h3", {text: node.label}), el("p", {text: node.reasonCode || node.templateId}));
      nodeActions.forEach(item => cardNode.append(button(item.label || item.name, () => openAction(run, semanticProjection, node, item), true)));
      canvas.append(cardNode);

      const listNode = el("li", {"data-runtime-node-id": node.runtimeNodeId}, [status(node.status), el("h3", {text: node.label}), mono(node.runtimeNodeId), el("p", {class: "dependency-list", text: node.dependencies?.length ? `上游：${node.dependencies.join(", ")}` : "上游：无（root）"})]);
      if (node.reasonCode) listNode.append(el("p", {class: "dependency-list", text: `原因：${node.reasonCode}`}));
      nodeActions.forEach(item => listNode.append(button(item.label || item.name, () => openAction(run, semanticProjection, node, item), true)));
      list.append(listNode);
    });
  }

  const publishDialog = document.querySelector("[data-publish-dialog]");
  const publishForm = document.querySelector("[data-publish-form]");
  let publishWorker = "";
  let publishOperationKey = "";
  function openPublish(workerName) {
    publishWorker = workerName;
    publishOperationKey = crypto.randomUUID();
    publishDialog.showModal();
    publishForm.elements.version.focus();
  }
  publishForm.addEventListener("input", () => { publishOperationKey = crypto.randomUUID(); });
  publishForm.addEventListener("submit", async event => {
    event.preventDefault();
    try {
      const bodyValue = {
        version: publishForm.elements.version.value, description: publishForm.elements.description.value, image: publishForm.elements.image.value,
        versionConfig: JSON.parse(publishForm.elements.versionConfig.value), runtime: {cpu: publishForm.elements.cpu.value, memory: publishForm.elements.memory.value},
      };
      if (!publishOperationKey) publishOperationKey = crypto.randomUUID();
      const result = await api(`/api/v1/workers/${encodeURIComponent(publishWorker)}/versions`, {
        method: "POST", headers: {"Idempotency-Key": publishOperationKey}, body: JSON.stringify(bodyValue),
      });
      publishOperationKey = "";
      publishDialog.close(); showNotice(`发布已受理：${result.operation.id}`, true); pollPublish(result.operation.statusUrl);
    } catch (error) { handleError(error); }
  });
  async function pollPublish(url) {
    const result = await api(url);
    if (result.operation.state === "running") return setTimeout(() => pollPublish(url).catch(handleError), 1200);
    if (result.operation.state === "succeeded") location.href = `/workers/${encodeURIComponent(publishWorker)}/versions/${encodeURIComponent(result.operation.workerVersion.version)}`;
    else showNotice(`发布失败：${result.operation.error?.message || "unknown"}`, true);
  }

  const triggerDialog = document.querySelector("[data-trigger-dialog]");
  const triggerForm = document.querySelector("[data-trigger-form]");
  const triggerPayload = document.querySelector("[data-trigger-payload]");
  const triggerError = document.querySelector("[data-trigger-error]");
  const triggerSchemaReference = document.querySelector("[data-trigger-schema-reference]");
  const triggerExample = document.querySelector("[data-trigger-example]");
  let triggerContext = null;
  function clearTriggerError() {
    triggerError.textContent = "";
    triggerError.hidden = true;
    triggerPayload.removeAttribute("aria-invalid");
  }
  function showTriggerError(message) {
    triggerError.textContent = message;
    triggerError.hidden = false;
    triggerPayload.setAttribute("aria-invalid", "true");
    triggerPayload.focus();
  }
  function formatTriggerPayload(value, format) {
    if (format === "json") return JSON.stringify(JSON.parse(payloadCodec.canonicalJSON(value)), null, 2);
    const rendered = payloadCodec.render(value);
    if (!rendered.ok) throw new Error("Payload 无法安全转换为 YAML。");
    return rendered.text;
  }
  function openTrigger(workerName, workerVersion, workflowName, workflowContract) {
    const schema = workflowContract.inputSchema || {};
    triggerContext = {workerName, workerVersion, workflow: workflowName, schema, example: exampleFromSchema(schema)};
    triggerForm.reset();
    triggerForm.elements.workerVersion.value = workerVersion;
    triggerForm.elements.inputFormat.value = "yaml";
    triggerPayload.value = formatTriggerPayload(triggerContext.example, "yaml");
    clearTriggerError();
    clear(triggerSchemaReference);
    triggerSchemaReference.append(yamlView(schema, "Input schema"));
    triggerDialog.showModal();
    triggerPayload.focus();
  }
  triggerPayload.addEventListener("input", clearTriggerError);
  triggerExample.addEventListener("click", () => {
    triggerPayload.value = formatTriggerPayload(triggerContext.example, triggerForm.elements.inputFormat.value);
    clearTriggerError();
    triggerPayload.focus();
  });
  triggerForm.elements.inputFormat.addEventListener("change", event => {
    const previous = event.target.value === "json" ? "yaml" : "json";
    const parsed = payloadCodec.parse(previous, triggerPayload.value);
    if (!parsed.ok) {
      event.target.value = previous;
      showTriggerError(parsed.error);
      return;
    }
    triggerPayload.value = formatTriggerPayload(parsed.value, event.target.value);
    clearTriggerError();
  });
  triggerForm.addEventListener("submit", async event => {
    event.preventDefault();
    const parsed = payloadCodec.parse(triggerForm.elements.inputFormat.value, triggerPayload.value);
    if (!parsed.ok) {
      showTriggerError(parsed.error);
      return;
    }
    try {
      const operationKey = crypto.randomUUID();
      const result = await api(`/api/v1/workers/${encodeURIComponent(triggerContext.workerName)}/workflows/${encodeURIComponent(triggerContext.workflow)}/runs`, {
        method: "POST", headers: {"Idempotency-Key": operationKey}, body: JSON.stringify({workerVersion: triggerForm.elements.workerVersion.value || undefined, description: triggerForm.elements.description.value, input: parsed.value}),
      });
      location.href = `/runs/${encodeURIComponent(result.run.id)}`;
    } catch (error) { showTriggerError(error.message || "Run 启动失败。"); }
  });

  const actionDialog = document.querySelector("[data-action-dialog]");
  const actionForm = document.querySelector("[data-action-form]");
  async function openAction(run, semanticProjection, node, allowedAction) {
    try {
      const {workerVersion} = await api(`/api/v1/workers/${encodeURIComponent(run.workerName)}/versions/${encodeURIComponent(run.selectedVersion)}`);
      const workflow = (workerVersion.contract.workflows || []).find(item => item.name === run.workflow);
      const actionContract = (workflow?.actions || []).find(item => item.name === allowedAction.name);
      if (!actionContract) throw new Error("Action contract 不存在或已变化");
      actionContext = {run, semanticProjection, node, allowedAction, actionContract, operationKey: crypto.randomUUID()};
      document.querySelector("[data-action-schema]").textContent = `${node.label} / ${allowedAction.label || allowedAction.name} · requiredPermission: ${actionContract.requiredPermission}`;
      buildSchemaFields(actionContract.inputSchema || {}, document.querySelector("[data-action-schema-fields]"), actionForm.elements.actionInput);
      actionDialog.showModal(); actionForm.querySelector("[data-schema-property]")?.focus();
    } catch (error) { handleError(error); }
  }
  actionForm.addEventListener("submit", async event => {
    event.preventDefault();
    const context = actionContext;
    try {
      const result = await api(`/api/v1/runs/${encodeURIComponent(context.run.id)}/nodes/${encodeURIComponent(context.node.runtimeNodeId)}/actions/${encodeURIComponent(context.allowedAction.name)}`, {
        method: "POST",
        headers: {"Idempotency-Key": context.operationKey, "If-Match": `"projection-r${context.semanticProjection.projectionRevision}"`},
        body: JSON.stringify({input: JSON.parse(actionForm.elements.actionInput.value)}),
      });
      const stateBox = document.querySelector("[data-delivery-state]");
      stateBox.hidden = false; stateBox.textContent = result.operation.state === "delivery-unknown" ? "送达结果未知。可使用同一操作 key 安全查询或重试；请勿创建新操作。" : `送达状态：${result.operation.state}。等待 Workflow 确认。`;
      pollAction(result.operation.statusUrl, context.operationKey);
    } catch (error) {
      if (error.code === "projection_conflict") showNotice("Run 已变化。已保留输入，请刷新后重新确认。", true);
      else handleError(error);
    }
  });
  async function pollAction(url, operationKey) {
    if (actionContext?.operationKey !== operationKey) return;
    const result = await api(url);
    const stateBox = document.querySelector("[data-delivery-state]");
    stateBox.hidden = false; stateBox.textContent = `操作状态：${result.operation.state}`;
    if (["accepted-by-workflow", "rejected-by-workflow"].includes(result.operation.state)) {
      actionDialog.close(); await renderRun(false); return;
    }
    setTimeout(() => pollAction(url, operationKey).catch(handleError), 1500);
  }

  document.querySelectorAll("[data-dialog-close]").forEach(close => close.addEventListener("click", () => close.closest("dialog").close()));
  document.querySelector("[data-refresh]").addEventListener("click", () => load().catch(handleError));
  document.addEventListener("visibilitychange", () => {
		if (document.hidden) return;
		if (page === "run") renderRun(true).catch(handleError);
		if (page === "runs") renderRuns(true).catch(handleError);
	});
  window.addEventListener("beforeunload", () => { window.clearInterval(runPoll); window.clearInterval(runsPoll); });

  function handleError(error) {
    console.error(error);
    showNotice(error.message || "请求失败。", true);
    content.setAttribute("aria-busy", "false");
  }

  async function load() {
    content.setAttribute("aria-busy", "true");
		const renderers = {overview: renderOverview, tenants: renderTenants, tenant: renderTenant, workers: renderWorkers, worker: renderWorker, version: renderVersion, workflows: renderWorkflows, workflow: renderWorkflow, runs: renderRuns, run: renderRun};
    await renderers[page]();
    content.setAttribute("aria-busy", "false");
  }

  load().catch(handleError);
})();
