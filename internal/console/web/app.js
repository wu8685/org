(() => {
  "use strict";

  const body = document.body;
  const page = body.dataset.page;
  const content = document.querySelector("[data-page-content]");
  const notice = document.querySelector("[data-notice]");
  const actions = document.querySelector("[data-heading-actions]");
  const csrf = document.querySelector('meta[name="csrf-token"]').content;
  const segments = location.pathname.split("/").filter(Boolean).map(decodeURIComponent);
  let lastRunETag = "";
  let actionContext = null;
  let runPoll = 0;

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
  const status = value => el("span", {class: `status status-${String(value || "pending").toLowerCase()}`, text: value || "unknown"});
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
      ["Active releases", `${overview.quotaUsage.activeReleases} / ${overview.quotaPolicy.maxActiveReleases}`],
      ["Worker Pods", `${overview.quotaUsage.activeWorkerPods} / ${overview.quotaPolicy.maxActiveWorkerPods}`],
      ["Reserved CPU", `${overview.quotaUsage.reservedCpuMilli}m / ${overview.quotaPolicy.maxReservedCPU}`],
      ["Reserved memory", `${overview.quotaUsage.reservedMemoryBytes} bytes / ${overview.quotaPolicy.maxReservedMemory}`],
    ]))));
  }

  async function renderWorkers() {
    const {items} = await api("/api/v1/workers");
    clear(content); clear(actions);
    actions.append(button("创建 Worker", createWorker));
    if (!items.length) return content.append(empty("当前 Tenant 还没有 Worker。"));
    content.append(table(["Worker", "Current", "Created"], items.map(worker => [
      link(worker.workerName || worker.name, `/workers/${encodeURIComponent(worker.workerName || worker.name)}`),
      mono(worker.currentVersion || "尚未发布"),
      new Date(worker.createdAt).toLocaleString(),
    ])));
  }

  async function createWorker() {
    const workerName = window.prompt("Worker name");
    if (!workerName) return;
    await api("/api/v1/workers", {method: "POST", body: JSON.stringify({workerName})});
    location.href = `/workers/${encodeURIComponent(workerName)}`;
  }

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
    ])) : empty("还没有 WorkerVersion。")));
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
    content.append(section("Runtime config", card([definition([["CPU", version.runtime.cpu], ["Memory", version.runtime.memory]]), el("pre", {class: "contract-view", text: JSON.stringify(version.versionConfig || {}, null, 2)})])));
    content.append(section("Read-only SDK contract", el("pre", {class: "contract-view", "data-contract-readonly": "", text: JSON.stringify(version.contract, null, 2)})));
  }

  async function renderWorkflows() {
    const {items} = await api("/api/v1/workflows");
    clear(content);
    if (!items.length) return content.append(empty("没有 Ready WorkerVersion 提供 Workflow。"));
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
    content.append(section("Read-only input contract", el("pre", {class: "contract-view", "data-contract-readonly": "", text: JSON.stringify(workflow.inputSchema || {}, null, 2)})));
  }

  async function renderRuns() {
    const {items} = await api("/api/v1/runs" + location.search);
    clear(content);
    if (!items.length) return content.append(empty("当前筛选没有 Run。"));
    content.append(table(["Run", "Worker", "Workflow", "Selected version", "Created"], items.map(run => [
      link(run.id, `/runs/${encodeURIComponent(run.id)}`), run.workerName, run.workflow, mono(run.selectedVersion), new Date(run.createdAt).toLocaleString(),
    ])));
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
      card([el("h2", {text: "Run"}), definition([["Run ID", mono(run.id)], ["Worker", run.workerName], ["Workflow", run.workflow], ["Selected version", mono(run.selectedVersion)], ["Release description", response.workerVersion.description]])]),
      card([el("h2", {text: "Live status"}), definition([["Execution", status(response.execution.status)], ["Projection", projection ? status(projection.runStatus) : "Unavailable"], ["Projection revision", projection?.projectionRevision ?? "—"], ["Allowed actions", projection?.allowedActions?.length || 0]])]),
    ]));
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
  function openPublish(workerName) { publishWorker = workerName; publishDialog.showModal(); publishForm.elements.version.focus(); }
  publishForm.addEventListener("submit", async event => {
    event.preventDefault();
    try {
      const bodyValue = {
        version: publishForm.elements.version.value, description: publishForm.elements.description.value, image: publishForm.elements.image.value,
        versionConfig: JSON.parse(publishForm.elements.versionConfig.value), runtime: {cpu: publishForm.elements.cpu.value, memory: publishForm.elements.memory.value},
        source: {repository: publishForm.elements.repository.value, branch: publishForm.elements.branch.value, commit: publishForm.elements.commit.value, ciReference: publishForm.elements.ciReference.value},
      };
      const result = await api(`/api/v1/workers/${encodeURIComponent(publishWorker)}/versions`, {method: "POST", body: JSON.stringify(bodyValue)});
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
  let triggerContext = null;
  function openTrigger(workerName, workerVersion, workflowName, workflowContract) {
    triggerContext = {workerName, workerVersion, workflow: workflowName};
    triggerForm.elements.workerVersion.value = workerVersion;
    buildSchemaFields(workflowContract.inputSchema || {}, document.querySelector("[data-trigger-schema-fields]"), triggerForm.elements.input);
    triggerDialog.showModal();
    triggerForm.querySelector("[data-schema-property]")?.focus();
  }
  triggerForm.addEventListener("submit", async event => {
    event.preventDefault();
    try {
      const operationKey = crypto.randomUUID();
      const result = await api(`/api/v1/workers/${encodeURIComponent(triggerContext.workerName)}/workflows/${encodeURIComponent(triggerContext.workflow)}/runs`, {
        method: "POST", headers: {"Idempotency-Key": operationKey}, body: JSON.stringify({workerVersion: triggerForm.elements.workerVersion.value || undefined, input: JSON.parse(triggerForm.elements.input.value)}),
      });
      location.href = `/runs/${encodeURIComponent(result.run.id)}`;
    } catch (error) { handleError(error); }
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
      buildSchemaFields(actionContract.inputSchema || {}, document.querySelector("[data-action-schema-fields]"), actionForm.elements.input);
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
        body: JSON.stringify({input: JSON.parse(actionForm.elements.input.value)}),
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
  document.addEventListener("visibilitychange", () => { if (!document.hidden && page === "run") renderRun(true).catch(handleError); });
  window.addEventListener("beforeunload", () => window.clearInterval(runPoll));

  function handleError(error) {
    console.error(error);
    showNotice(error.message || "请求失败。", true);
    content.setAttribute("aria-busy", "false");
  }

  async function load() {
    content.setAttribute("aria-busy", "true");
    const renderers = {overview: renderOverview, workers: renderWorkers, worker: renderWorker, version: renderVersion, workflows: renderWorkflows, workflow: renderWorkflow, runs: renderRuns, run: renderRun};
    await renderers[page]();
    content.setAttribute("aria-busy", "false");
  }

  load().catch(handleError);
})();
