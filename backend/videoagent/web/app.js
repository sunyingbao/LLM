const state = {
  definitions: {},
  nodes: [],
  edges: [],
  tool: "select",
  selected: null,
  selectedEdge: null,
  connectFrom: null,
  run: null,
  artifacts: [],
  finalAnnouncedRunID: null,
  timer: null,
  nextNode: 1,
  conversationID: localStorage.getItem("video-agent-conversation") || "",
  runID: localStorage.getItem("video-agent-run") || "",
};

const defaultNodes = [
  ["requirement", "requirement", 70, 170],
  ["clipscript", "clipscript", 315, 170],
  ["competition", "competition_reference_image", 570, 50],
  ["tts", "prompt_tts", 570, 340],
  ["character_reference", "character_reference_image", 570, 630],
  ["preview", "preview", 835, 340],
  ["finalvideo", "finalvideo", 1080, 340],
].map(([id, kind, x, y]) => ({ id, kind, x, y }));

const defaultEdges = [
  ["requirement", "requirement", "clipscript", "requirement"],
  ["clipscript", "clipscript", "competition", "clipscript"],
  ["clipscript", "clipscript", "tts", "clipscript"],
  ["clipscript", "clipscript", "character_reference", "clipscript"],
  ["clipscript", "clipscript", "preview", "clipscript"],
  ["competition", "competition_reference_image", "preview", "resources"],
  ["tts", "voice_preview", "preview", "resources"],
  ["character_reference", "character_reference_image", "preview", "resources"],
  ["preview", "preview_video", "finalvideo", "preview_video"],
	["tts", "voice_preview", "finalvideo", "resources"],
	["clipscript", "clipscript", "finalvideo", "clipscript"],
].map(([fromNode, fromPort, toNode, toPort]) => ({ fromNode, fromPort, toNode, toPort }));

const $ = (id) => document.getElementById(id);

async function loadDefinitions() {
  state.definitions = await fetchJSON("/workflow/node-definitions");
  const select = $("node-kind");
  const palette = $("node-palette");
  Object.keys(state.definitions).forEach((kind) => {
    const option = document.createElement("option");
    option.value = kind;
    option.textContent = kind;
    select.appendChild(option);

    const item = document.createElement("div");
    item.className = "palette-node";
    item.draggable = true;
    item.dataset.kind = kind;
    item.textContent = kind;
    item.addEventListener("dragstart", (event) => event.dataTransfer.setData("text/node-kind", kind));
    palette.appendChild(item);
  });
}

async function loadProjectWorkflow() {
  const project = await fetchJSON("/projects/demo");
  const version = project.workflow_versions?.find((item) => item.workflow_version_id === project.current_workflow_version)
    || project.workflow_versions?.[project.workflow_versions.length - 1];
  if (!version) {
    resetWorkflow();
    return;
  }
  state.nodes = (version.nodes || []).map((node, index) => ({
    id: node.node_id,
    kind: node.kind,
    config: node.config,
    x: node.layout?.x || defaultNodes.find((item) => item.id === node.node_id)?.x || 80 + (index % 3) * 230,
    y: node.layout?.y || defaultNodes.find((item) => item.id === node.node_id)?.y || 80 + Math.floor(index / 3) * 170,
  }));
  state.edges = (version.edges || []).map((edge) => ({
    fromNode: edge.from_node,
    fromPort: edge.from_port,
    toNode: edge.to_node,
    toPort: edge.to_port,
  }));
  render();
}

async function restoreConversation() {
	if (!state.conversationID) return;
	const messages = await fetchJSON(`/conversations/${state.conversationID}/messages`);
	for (const message of messages) {
		const text = message.parts?.filter((part) => part.type === "text").map((part) => part.text).filter(Boolean).join("\n");
		if (text) addMessage(text, message.role === "assistant" ? "assistant" : "user");
		for (const part of message.parts || []) {
			if (part.type !== "operation" || !part.operation_id) continue;
			const operation = await fetchJSON(`/operations/${part.operation_id}`);
			renderOperation(operation);
			if (operation.run_id) state.runID = operation.run_id;
		}
	}
}

async function restoreSession() {
	const session = await fetchJSON("/projects/demo/session");
	state.conversationID = session.conversation?.conversation_id || "";
	state.runID = session.run_id || "";
	if (state.conversationID) localStorage.setItem("video-agent-conversation", state.conversationID);
	else localStorage.removeItem("video-agent-conversation");
	if (state.runID) localStorage.setItem("video-agent-run", state.runID);
	else localStorage.removeItem("video-agent-run");
}

async function restoreRun() {
  if (!state.runID) return;
  try {
    state.run = await fetchJSON(`/runs/${state.runID}`);
    updateRun(state.run);
    if (!runFinished(state.run)) state.timer = setInterval(refreshRun, 500);
  } catch (_) {
    localStorage.removeItem("video-agent-run");
    state.runID = "";
  }
}

function resetWorkflow() {
  state.nodes = defaultNodes.map((node) => ({ ...node }));
  state.edges = defaultEdges.map((edge) => ({ ...edge }));
  state.selected = null;
  state.selectedEdge = null;
  state.connectFrom = null;
  state.artifacts = [];
  render();
}

function render() {
  const container = $("nodes");
  container.replaceChildren();
  state.nodes.forEach((node) => {
    const element = document.createElement("article");
    element.className = `node ${node.state || ""} ${state.selected === node.id ? "selected" : ""}`;
    element.dataset.nodeId = node.id;
    element.style.left = `${node.x}px`;
    element.style.top = `${node.y}px`;
    const head = document.createElement("div");
    head.className = "node-head";
    const dot = document.createElement("span");
    dot.className = "node-dot";
    const title = document.createElement("strong");
    title.textContent = node.id;
    head.append(dot, title);
    const kind = document.createElement("div");
    kind.className = "node-kind";
    kind.textContent = node.kind;
    const nodeState = document.createElement("div");
    nodeState.className = "node-state";
    nodeState.textContent = node.state || "待运行";
    element.append(head, kind, nodeState);
    if (node.artifactResults?.length) {
      if (node.artifactResults.some((artifact) => artifact.kind === "requirement")) {
        element.classList.add("has-requirement-result");
      }
      const results = document.createElement("div");
      results.className = "node-results";
      results.addEventListener("pointerdown", (event) => event.stopPropagation());
      node.artifactResults.forEach((artifact) => results.appendChild(createArtifactContent(artifact, true)));
      element.appendChild(results);
    }
    element.addEventListener("pointerdown", (event) => startNodeInteraction(event, node));
    container.appendChild(element);
  });
  renderEdges();
}

function startNodeInteraction(event, node) {
  event.stopPropagation();
	state.selectedEdge = null;
  if (state.tool === "connect") {
    if (!state.connectFrom) {
      state.connectFrom = node.id;
      state.selected = node.id;
      render();
      addMessage(`选择了 ${node.id}，请点击目标节点完成连线`, "assistant");
      return;
    }
    if (state.connectFrom !== node.id) connectNodes(state.connectFrom, node.id);
    state.connectFrom = null;
    return;
  }
  state.selected = node.id;
  const startX = event.clientX;
  const startY = event.clientY;
  const originalX = node.x;
  const originalY = node.y;
  const move = (moveEvent) => {
    node.x = Math.max(8, originalX + moveEvent.clientX - startX);
    node.y = Math.max(8, originalY + moveEvent.clientY - startY);
    render();
  };
  const stop = () => {
    document.removeEventListener("pointermove", move);
    document.removeEventListener("pointerup", stop);
  };
  document.addEventListener("pointermove", move);
  document.addEventListener("pointerup", stop);
  render();
}

function connectNodes(fromNode, toNode) {
  const source = state.definitions[state.nodes.find((node) => node.id === fromNode)?.kind];
  const target = state.definitions[state.nodes.find((node) => node.id === toNode)?.kind];
  const output = source?.outputs?.[0];
  const input = target?.inputs?.find((candidate) => candidate.artifact_kind === output?.artifact_kind) || target?.inputs?.[0];
  if (!output || !input) {
    addMessage("这两个节点没有可连接的端口", "error");
    return;
  }
  const exists = state.edges.some((edge) => edge.fromNode === fromNode && edge.toNode === toNode && edge.toPort === input.name);
  if (!exists) state.edges.push({ fromNode, fromPort: output.name, toNode, toPort: input.name });
  render();
}

function renderEdges() {
  const svg = $("edges");
  svg.replaceChildren();
  const lineagePairs = artifactLineagePairs();
  state.edges.forEach((edge, index) => {
    const from = state.nodes.find((node) => node.id === edge.fromNode);
    const to = state.nodes.find((node) => node.id === edge.toNode);
    if (!from || !to) return;
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    const x1 = from.x + 220;
    const y1 = from.y + 48;
    const x2 = to.x;
    const y2 = to.y + 48;
    const direction = x2 >= x1 ? 1 : -1;
    const bend = Math.min(80, Math.abs(x2 - x1) / 2);
    path.setAttribute("d", `M ${x1} ${y1} C ${x1 + direction * bend} ${y1}, ${x2 - direction * bend} ${y2}, ${x2} ${y2}`);
    path.classList.toggle("active", state.selectedEdge === index);
    path.classList.toggle("lineage", lineagePairs.has(`${from.id}->${to.id}`));
    path.addEventListener("pointerdown", (event) => {
      event.stopPropagation();
      state.selected = null;
      state.selectedEdge = index;
      render();
    });
    svg.appendChild(path);
  });
}

function artifactLineagePairs() {
  const owners = new Map();
  state.artifacts.forEach((artifact) => {
    const owner = state.nodes.find((node) => node.artifacts?.includes(artifactKey(artifact)));
    if (owner) owners.set(artifactKey(artifact), owner);
  });
  const pairs = new Set();
  state.artifacts.forEach((artifact) => {
    const target = owners.get(artifactKey(artifact));
    if (!target) return;
    artifact.parent_ids?.forEach((parentID) => {
      const source = owners.get(parentID);
      if (!source || source.id === target.id) return;
      pairs.add(`${source.id}->${target.id}`);
    });
  });
  return pairs;
}

function workflowPayload() {
  return {
    nodes: state.nodes.map((node) => ({ node_id: node.id, kind: node.kind, config: node.config, layout: { x: node.x, y: node.y } })),
    edges: state.edges.map((edge) => ({ from_node: edge.fromNode, from_port: edge.fromPort, to_node: edge.toNode, to_port: edge.toPort })),
  };
}

function productImageURLs() {
  return $("product-images").value.split(/\s*[\n,]\s*/).map((url) => url.trim()).filter(Boolean);
}

function addNode(kind, x, y) {
  if (!state.definitions[kind]) return;
  const id = `${kind}-${state.nextNode++}`;
  state.nodes.push({ id, kind, x: Math.max(8, x), y: Math.max(8, y) });
  state.selected = id;
	state.selectedEdge = null;
  render();
}

function deleteSelection() {
  if (state.selectedEdge !== null) {
	state.edges.splice(state.selectedEdge, 1);
	state.selectedEdge = null;
	render();
	return true;
  }
  if (!state.selected) return false;
	state.nodes = state.nodes.filter((node) => node.id !== state.selected);
	state.edges = state.edges.filter((edge) => edge.fromNode !== state.selected && edge.toNode !== state.selected);
	state.selected = null;
	render();
	return true;
}

async function saveWorkflow() {
  try {
    const operation = await fetchJSON("/projects/demo/operations", {
      method: "POST",
      headers: { "Idempotency-Key": operationKey("workflow") },
      body: JSON.stringify({
        project_id: "demo",
        type: "update_workflow",
        payload: workflowPayload(),
      }),
    });
    renderOperation(operation);
    addMessage("画布修改已提交，确认后才会保存为新版本。", "assistant");
  } catch (error) {
    addMessage(error.message, "error");
  }
}

async function runWorkflow() {
  addMessage($("brief").value, "user");
  const payload = {
    product_name: $("product-name").value,
    product_image_urls: productImageURLs(),
    brief: $("brief").value,
  };
  try {
    const operation = await fetchJSON("/projects/demo/operations", {
      method: "POST",
      headers: { "Idempotency-Key": operationKey("run") },
      body: JSON.stringify({ type: "run", payload }),
    });
    renderOperation(operation);
    setStatus("待确认", "running");
  } catch (error) {
    addMessage(error.message, "error");
  }
}

async function sendAgentMessage() {
  const message = $("agent-message").value.trim();
  if (!message) return;
  addMessage(message, "user");
  $("agent-message").value = "";
  try {
    const response = await fetchJSON("/agent/chat", {
      method: "POST",
		headers: { "Idempotency-Key": operationKey("chat") },
      body: JSON.stringify({ project_id: "demo", conversation_id: state.conversationID, run_id: state.run?.run_id || "", text: message, product_name: $("product-name").value, product_image_urls: productImageURLs(), brief: $("brief").value }),
    });
    state.conversationID = response.conversation?.conversation_id || state.conversationID;
    if (state.conversationID) localStorage.setItem("video-agent-conversation", state.conversationID);
    const messagePart = response.messages?.[response.messages.length - 1]?.parts?.find((part) => part.type === "text");
    addMessage(messagePart?.text || "Agent 已处理请求。", "assistant");
    if (response.operation) renderOperation(response.operation);
  } catch (error) {
    addMessage(error.message, "error");
  }
}

async function retryRun() {
  if (!state.run) {
    addMessage("当前没有可重试的运行。", "error");
    return;
  }
  try {
    const operation = await fetchJSON("/projects/demo/operations", {
      method: "POST",
      headers: { "Idempotency-Key": operationKey(`retry:${state.run.run_id}`) },
      body: JSON.stringify({ type: "retry", run_id: state.run.run_id, payload: { run_id: state.run.run_id } }),
    });
    renderOperation(operation);
    addMessage("重试操作已提交，请确认后继续。", "assistant");
  } catch (error) {
    addMessage(error.message, "error");
  }
}

function renderOperation(operation) {
	const element = document.createElement("div");
	element.className = "message assistant";
	if (["applied", "rejected"].includes(operation.status)) {
		const labels = { applied: "操作已确认", rejected: "操作已拒绝" };
		element.textContent = `${labels[operation.status] || operation.status}：${operation.type}`;
		$("messages").appendChild(element);
		return;
	}
	const confirmed = operation.status === "confirmed";
	element.appendChild(document.createTextNode(`${confirmed ? "待继续操作" : "待确认操作"}：${operation.type} `));
  const confirm = document.createElement("button");
  confirm.className = "secondary";
  confirm.textContent = confirmed ? "继续" : "确认";
  const reject = document.createElement("button");
  reject.className = "secondary";
  reject.textContent = "拒绝";
  element.appendChild(confirm);
  if (!confirmed) element.appendChild(reject);
  confirm.addEventListener("click", async () => {
    try {
      const result = await fetchJSON(`/operations/${operation.operation_id}/confirm`, { method: "POST" });
      element.textContent = "操作已确认";
      if (result.run) {
        if (state.timer) clearInterval(state.timer);
        state.run = result.run;
        state.runID = state.run.run_id;
        localStorage.setItem("video-agent-run", state.runID);
        state.finalAnnouncedRunID = null;
        updateRun(state.run);
        if (!runFinished(state.run)) state.timer = setInterval(refreshRun, 500);
      } else {
        await loadProjectWorkflow();
      }
    } catch (error) { addMessage(error.message, "error"); }
  });
  if (!confirmed) {
    reject.addEventListener("click", async () => {
      try {
        await fetchJSON(`/operations/${operation.operation_id}/reject`, { method: "POST" });
        element.textContent = "操作已拒绝";
      } catch (error) { addMessage(error.message, "error"); }
    });
  }
  $("messages").appendChild(element);
}

async function refreshRun() {
  if (!state.run) return;
  try {
    state.run = await fetchJSON(`/runs/${state.run.run_id}`);
    updateRun(state.run);
    if (runFinished(state.run)) clearInterval(state.timer);
  } catch (error) {
    addMessage(error.message, "error");
    clearInterval(state.timer);
  }
}

function runFinished(run) {
  return run.canceled || run.node_runs.every((node) => ["succeeded", "failed", "canceled"].includes(node.state));
}

function updateRun(run) {
  const nodesByID = new Map(state.nodes.map((node) => [node.id, node]));
  const artifacts = [];
  state.nodes.forEach((node) => { node.artifacts = []; node.artifactResults = []; });
  run.node_runs.forEach((nodeRun) => {
    const node = nodesByID.get(nodeRun.node_id);
    if (node && !nodeRun.instance_key) node.state = nodeRun.state;
    (nodeRun.artifacts || []).forEach((artifact) => {
      artifacts.push(artifact);
      if (node) {
        node.artifacts.push(artifact.artifact_id || artifact.id);
        node.artifactResults.push(artifact);
      }
    });
  });
  state.artifacts = artifacts;
  render();
  renderArtifacts(artifacts);
	const final = artifacts.find((artifact) => artifact.kind === "finalvideo" && artifact.status === "succeeded");
	const failed = run.node_runs.some((node) => node.state === "failed");
	const retryableFailure = run.node_runs.some((node) => node.state === "failed" && !node.submission_unknown);
	const canceled = run.canceled || run.node_runs.some((node) => node.state === "canceled");
	$("retry-button").disabled = !retryableFailure || canceled;
	$("cancel-button").disabled = runFinished(run);
  setStatus(final ? "已完成" : canceled ? "已取消" : failed ? "执行失败" : "执行中", final ? "success" : canceled || failed ? "failed" : "running");
  if (final && state.finalAnnouncedRunID !== run.run_id) {
    state.finalAnnouncedRunID = run.run_id;
    addMessage("正式成片已生成，可以在右侧产物区查看。", "assistant");
  }
}

async function cancelRun() {
  if (!state.run) {
    addMessage("当前没有可取消的运行。", "error");
    return;
  }
  try {
    const operation = await fetchJSON("/projects/demo/operations", {
      method: "POST",
      headers: { "Idempotency-Key": operationKey(`cancel:${state.run.run_id}`) },
      body: JSON.stringify({ type: "cancel", run_id: state.run.run_id, payload: { run_id: state.run.run_id } }),
    });
    renderOperation(operation);
    addMessage("取消操作已提交，请确认后停止当前运行。", "assistant");
  } catch (error) {
    addMessage(error.message, "error");
  }
}

function renderArtifacts(artifacts) {
  const container = $("artifacts");
  container.replaceChildren();
  $("artifact-count").textContent = `${artifacts.length} 个`;
  artifacts.forEach((artifact) => {
    const element = document.createElement("div");
    element.className = "artifact";
    const title = document.createElement("div");
    title.className = "artifact-title";
    const kind = document.createElement("span");
    kind.className = "artifact-kind";
    kind.textContent = artifact.kind;
    const status = document.createElement("span");
    status.className = "artifact-status";
    status.textContent = artifact.status;
    title.append(kind, status);
    element.appendChild(title);
    if (artifact.parent_ids?.length) {
      const parents = document.createElement("div");
      parents.className = "artifact-parents";
      parents.textContent = `来源：${artifact.parent_ids.join("、")}`;
      element.appendChild(parents);
    }
    element.appendChild(createArtifactContent(artifact, false));
    container.appendChild(element);
  });
}

function createArtifactContent(artifact, compact) {
  const content = document.createElement("div");
  content.className = `artifact-content ${compact ? "compact" : ""}`;
  const label = document.createElement("div");
  label.className = "artifact-content-label";
  label.textContent = artifact.kind;
  content.appendChild(label);

  const data = artifact.data && typeof artifact.data === "object" ? artifact.data : {};
  let rendered = false;
  if (artifact.kind === "requirement") {
    content.classList.add("requirement-result");
    label.textContent = "模型需求分析";
    appendArtifactText(content, data.markdown || data.objective || "");
    if (data.audience) appendArtifactText(content, `受众：${data.audience}`);
    if (data.selling_points?.length) appendArtifactText(content, `卖点：${data.selling_points.join("、")}`);
    rendered = true;
  } else if (artifact.kind === "clipscript") {
    appendArtifactText(content, data.title || "分镜脚本");
    const scenes = document.createElement("ol");
    scenes.className = "artifact-scenes";
    (data.scenes || []).forEach((scene) => {
      const item = document.createElement("li");
      item.textContent = [scene.visual, scene.voiceover].filter(Boolean).join(" / ");
      scenes.appendChild(item);
    });
    if (scenes.childElementCount) content.appendChild(scenes);
    rendered = true;
  } else if (["competition_reference_image", "character_reference_image"].includes(artifact.kind)) {
    rendered = appendArtifactMedia(content, "img", data.url, data.uri, "图片结果");
  } else if (artifact.kind === "voice_preview") {
    rendered = appendArtifactMedia(content, "audio", data.preview_audio_url || data.example_audio_url || data.audio_url, data.preview_audio_uri || data.example_audio_uri || data.audio_uri, "音频结果");
  } else if (["preview_video", "finalvideo"].includes(artifact.kind)) {
    rendered = appendArtifactMedia(content, "video", data.url || data.preview_video_url || data.finalvideo_url, data.uri, "视频结果");
  }

  if (!rendered && artifact.message) appendArtifactText(content, artifact.message);
  if (!rendered && Object.keys(data).length) appendArtifactText(content, artifactDataSummary(data));
  return content;
}

function appendArtifactText(container, text) {
  if (!text) return;
  const element = document.createElement("div");
  element.className = "artifact-text";
  element.textContent = text;
  container.appendChild(element);
}

function appendArtifactMedia(container, type, rawURL, uri, label) {
  const url = safeArtifactURL(rawURL);
  if (!url || new URL(url).hostname === "local") {
    const placeholder = document.createElement("div");
    placeholder.className = `artifact-media-placeholder ${type}`;
    placeholder.textContent = `${label}（本地模拟）`;
    if (uri || rawURL) placeholder.title = uri || rawURL;
    container.appendChild(placeholder);
    return true;
  }

  const media = document.createElement(type);
  media.className = "artifact-media";
  media.src = url;
  if (type === "img") {
    media.alt = label;
    media.loading = "lazy";
  } else {
    media.controls = true;
    media.preload = "metadata";
    if (type === "video") media.playsInline = true;
  }
  media.addEventListener("error", () => {
    const placeholder = document.createElement("div");
    placeholder.className = `artifact-media-placeholder ${type}`;
    placeholder.textContent = `${label}加载失败`;
    media.replaceWith(placeholder);
  });
  container.appendChild(media);
  return true;
}

function artifactDataSummary(data) {
  return Object.entries(data)
    .filter(([, value]) => value !== null && value !== "" && !Array.isArray(value) && typeof value !== "object")
    .slice(0, 4)
    .map(([key, value]) => `${key}: ${value}`)
    .join("\n");
}

function artifactKey(artifact) {
  return artifact.artifact_id || artifact.id || "";
}

function addMessage(text, role) {
  const element = document.createElement("div");
  element.className = `message ${role}`;
  element.textContent = text;
  $("messages").appendChild(element);
  $("messages").scrollTop = $("messages").scrollHeight;
}

function setStatus(text, className) {
  const status = $("run-status");
  status.textContent = text;
  status.className = `status-pill ${className || ""}`;
}

async function fetchJSON(url, options = {}) {
  const response = await fetch(url, { ...options, headers: { "Content-Type": "application/json", ...(options.headers || {}) } });
  if (!response.ok) throw new Error((await response.json()).error || `请求失败: ${response.status}`);
  return response.json();
}

function operationKey(action) {
  return `${action}:${crypto.randomUUID()}`;
}

function safeArtifactURL(value) {
  if (!value) return "";
  try {
    const url = new URL(value, window.location.origin);
    return ["http:", "https:"].includes(url.protocol) ? url.href : "";
  } catch (_) {
    return "";
  }
}

document.querySelectorAll("[data-tool]").forEach((button) => button.addEventListener("click", () => {
  document.querySelectorAll("[data-tool]").forEach((item) => item.classList.remove("active"));
  button.classList.add("active");
  state.tool = button.dataset.tool;
  state.connectFrom = null;
}));
$("add-node").addEventListener("click", () => {
  const kind = $("node-kind").value;
  addNode(kind, 90 + (state.nodes.length % 3) * 230, 470 + Math.floor(state.nodes.length / 3) * 120);
});
$("delete-node").addEventListener("click", () => {
	if (!deleteSelection()) addMessage("请先选择要删除的节点或连线。", "error");
});
$("save-workflow").addEventListener("click", saveWorkflow);
$("reset-workflow").addEventListener("click", resetWorkflow);
$("run-button").addEventListener("click", runWorkflow);
$("retry-button").addEventListener("click", retryRun);
$("cancel-button").addEventListener("click", cancelRun);
$("send-agent").addEventListener("click", sendAgentMessage);
$("canvas").addEventListener("dragover", (event) => event.preventDefault());
$("canvas").addEventListener("drop", (event) => {
  event.preventDefault();
  const kind = event.dataTransfer.getData("text/node-kind");
  const bounds = $("canvas").getBoundingClientRect();
  addNode(kind, event.clientX - bounds.left, event.clientY - bounds.top);
});
$("canvas").addEventListener("pointerdown", () => { state.selected = null; state.selectedEdge = null; render(); });
document.addEventListener("keydown", (event) => {
	if (event.key === "Delete" && state.tool === "select") deleteSelection();
});

(async function init() {
  try {
		await loadDefinitions();
		await loadProjectWorkflow();
		await restoreSession();
		await restoreConversation();
    await restoreRun();
    if (!state.conversationID) addMessage("工作流已加载，可以直接运行或调整节点。", "assistant");
  } catch (error) {
    addMessage(error.message, "error");
  }
})();
