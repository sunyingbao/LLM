const projectID = new URLSearchParams(window.location.search).get("project_id")?.trim() || "demo";
const storageKey = (name) => `video-agent:${projectID}:${name}`;
const projectURL = (suffix = "") => `/projects/${encodeURIComponent(projectID)}${suffix}`;

const state = {
  projectID,
  definitions: {},
  nodes: [],
  edges: [],
  tool: "select",
  selected: null,
  selectedEdge: null,
  connectFrom: null,
  zoom: 1,
  history: [],
  historyIndex: -1,
  dirty: false,
  run: null,
  artifacts: [],
  artifactNodeID: null,
  primaryAction: "run",
  finalAnnouncedRunID: null,
  timer: null,
  nextNode: 1,
  conversationID: localStorage.getItem(storageKey("conversation")) || "",
  runID: localStorage.getItem(storageKey("run")) || "",
};

const nodeLabels = {
  requirement: "需求分析",
  clipscript: "分镜脚本",
  competition: "竞品图",
  competition_reference_image: "竞品图",
  tts: "TTS 音色",
  prompt_tts: "TTS 音色",
  character_reference: "人物参考图",
  character_reference_image: "人物参考图",
  preview: "视频预览",
  finalvideo: "正式成片",
};

const artifactLabels = {
  requirement: "需求文档",
  clipscript: "分镜脚本",
  competition_reference_image: "竞品图集",
  voice_preview: "音色预览",
  character_reference_image: "人物参考图",
  preview_video: "视频预览",
  finalvideo: "正式成片",
};

const operationLabels = {
  run: "生成视频",
  retry: "重试运行",
  cancel: "取消运行",
  update_workflow: "保存画布",
};

const stateLabels = {
  pending: "等待中",
  waiting: "等待依赖",
  running: "生成中",
  succeeded: "已完成",
  failed: "执行失败",
  canceled: "已取消",
};

const internalArtifactKinds = new Set(["clipscript_annotation"]);

const defaultNodes = [
  ["requirement", "requirement", 95, 82],
  ["clipscript", "clipscript", 430, 82],
  ["competition", "competition_reference_image", 105, 370],
  ["tts", "prompt_tts", 430, 370],
  ["character_reference", "character_reference_image", 755, 370],
  ["preview", "preview", 430, 665],
  ["finalvideo", "finalvideo", 780, 665],
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

async function loadRuntime() {
  const runtime = $("runtime-mode");
  try {
    const status = await fetchJSON("/runtime");
    const enabled = [status.image && "图像", status.tts && "TTS", status.preview && "预览", status.finalvideo && "成片"].filter(Boolean);
    runtime.textContent = status.mode === "remote" ? "运行环境：远端直连" : "运行环境：本地模拟";
    runtime.title = enabled.length ? `已装配：${enabled.join("、")}` : "未装配媒体能力";
  } catch (_) {
    runtime.textContent = "运行环境：未知";
  }
}

async function loadDefinitions() {
  state.definitions = await fetchJSON("/workflow/node-definitions");
  const select = $("node-kind");
  const palette = $("node-palette");
  Object.keys(state.definitions).forEach((kind) => {
    const option = document.createElement("option");
    option.value = kind;
    option.textContent = nodeLabels[kind] || kind;
    select.appendChild(option);

    const item = document.createElement("div");
    item.className = "palette-node";
    item.draggable = true;
    item.dataset.kind = kind;
    item.textContent = nodeLabels[kind] || kind;
    item.addEventListener("dragstart", (event) => event.dataTransfer.setData("text/node-kind", kind));
    palette.appendChild(item);
  });
}

async function loadProjectWorkflow() {
  let project;
  try {
    project = await fetchJSON(projectURL());
  } catch (error) {
    if (!error.message.includes("project not found")) throw error;
    resetWorkflow(false);
    return;
  }
  if (project.name && project.name !== project.project_id) $("project-name").textContent = project.name;
  const version = project.workflow_versions?.find((item) => item.workflow_version_id === project.current_workflow_version)
    || project.workflow_versions?.[project.workflow_versions.length - 1];
  if (!version) {
    resetWorkflow(false);
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
  resetHistory();
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
	const session = await fetchJSON(projectURL("/session"));
	state.conversationID = session.conversation?.conversation_id || "";
	state.runID = session.run_id || "";
	if (state.conversationID) localStorage.setItem(storageKey("conversation"), state.conversationID);
	else localStorage.removeItem(storageKey("conversation"));
	if (state.runID) localStorage.setItem(storageKey("run"), state.runID);
	else localStorage.removeItem(storageKey("run"));
}

async function restoreRun() {
  if (!state.runID) return;
  try {
    state.run = await fetchJSON(`/runs/${state.runID}`);
    updateRun(state.run);
    if (!runFinished(state.run)) state.timer = setInterval(refreshRun, 500);
  } catch (_) {
    localStorage.removeItem(storageKey("run"));
    state.runID = "";
  }
}

function resetWorkflow(changed = true) {
  state.nodes = defaultNodes.map((node) => ({ ...node }));
  state.edges = defaultEdges.map((edge) => ({ ...edge }));
  state.selected = null;
  state.selectedEdge = null;
  state.connectFrom = null;
  state.artifacts = [];
  resetHistory();
  if (changed) markDirty();
  render();
}

function workflowSnapshot() {
	return JSON.stringify({
		nodes: state.nodes.map((node) => ({ id: node.id, kind: node.kind, config: node.config, x: node.x, y: node.y })),
		edges: state.edges.map((edge) => ({ ...edge })),
	});
}

function resetHistory() {
  state.history = [workflowSnapshot()];
  state.historyIndex = 0;
  state.dirty = false;
  updateHistoryButtons();
  setSaveStatus("已保存");
}

function recordHistory() {
  const snapshot = workflowSnapshot();
  if (state.history[state.historyIndex] === snapshot) return;
  state.history = state.history.slice(0, state.historyIndex + 1);
  state.history.push(snapshot);
  state.historyIndex = state.history.length - 1;
  markDirty();
  updateHistoryButtons();
}

function restoreHistory(index) {
  const snapshot = state.history[index];
  if (!snapshot) return;
  const workflow = JSON.parse(snapshot);
  state.nodes = workflow.nodes || [];
  state.edges = workflow.edges || [];
  state.historyIndex = index;
  state.selected = null;
  state.selectedEdge = null;
  state.connectFrom = null;
  markDirty();
  updateHistoryButtons();
  render();
}

function updateHistoryButtons() {
  $("undo-button").disabled = state.historyIndex <= 0;
  $("redo-button").disabled = state.historyIndex < 0 || state.historyIndex >= state.history.length - 1;
}

function markDirty() {
  state.dirty = true;
  setSaveStatus("未保存");
  if (!["pending", "running"].includes(state.primaryAction)) setPrimaryAction("save");
}

function setSaveStatus(text) {
  const status = $("save-status");
  if (status) status.textContent = text === "已保存" ? "◉ 已保存" : `○ ${text}`;
}

function applyZoom(value) {
  state.zoom = Math.min(1.6, Math.max(0.6, value));
  $("canvas-stage").style.transform = `scale(${state.zoom})`;
  $("zoom-value").textContent = `${Math.round(state.zoom * 100)}%`;
  renderEdges();
}

function changeZoom(delta) {
  applyZoom(state.zoom + delta);
}

function fitCanvas() {
  applyZoom(1);
  $("canvas").scrollLeft = 0;
  $("canvas").scrollTop = 0;
}

function exportWorkflow() {
  const blob = new Blob([JSON.stringify(workflowPayload(), null, 2)], { type: "application/json" });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `${($("project-name").textContent || "video-agent").trim()}.workflow.json`;
  link.click();
  URL.revokeObjectURL(link.href);
}

function render() {
  const container = $("nodes");
  $("canvas").classList.toggle("connect-mode", state.tool === "connect");
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
    title.textContent = nodeLabels[node.id] || nodeLabels[node.kind] || node.id;
    const code = document.createElement("span");
    code.className = "node-code";
    code.textContent = node.kind;
    title.appendChild(code);
    const nodeState = document.createElement("div");
    nodeState.className = "node-state";
    nodeState.textContent = stateLabels[node.state] || "待运行";
    head.append(dot, title, nodeState);
    element.append(head, renderPorts(node));
    if (node.message && ["running", "failed"].includes(node.state)) {
      const message = document.createElement("div");
      message.className = "node-message";
      message.textContent = node.message;
      element.appendChild(message);
    }
    if (node.artifactResults?.length) {
      if (node.artifactResults.some((artifact) => artifact.kind === "requirement")) {
        element.classList.add("has-requirement-result");
      }
      const results = document.createElement("div");
      results.className = "node-results";
      results.addEventListener("pointerdown", (event) => event.stopPropagation());
      const shownArtifacts = ["requirement", "clipscript"].includes(node.kind)
        ? node.artifactResults
        : node.artifactResults.slice(0, 1);
      shownArtifacts.forEach((artifact) => results.appendChild(createArtifactContent(artifact, true)));
      if (node.artifactResults.length > shownArtifacts.length) {
        const count = document.createElement("div");
        count.className = "node-result-count";
        count.textContent = `共 ${node.artifactResults.length} 个结果，可在右侧查看全部`;
        results.appendChild(count);
      }
      element.appendChild(results);
    }
    element.addEventListener("pointerdown", (event) => startNodeInteraction(event, node));
    container.appendChild(element);
  });
  renderEdges();
}

function renderPorts(node) {
  const definition = state.definitions[node.kind] || {};
  const ports = document.createElement("div");
  ports.className = "node-ports";
  const inputs = document.createElement("div");
  inputs.className = "node-port-group input-group";
  const outputs = document.createElement("div");
  outputs.className = "node-port-group output-group";
  for (const port of definition.inputs || []) inputs.appendChild(renderPort(node, port, "input"));
  for (const port of definition.outputs || []) outputs.appendChild(renderPort(node, port, "output"));
  ports.append(inputs, outputs);
  return ports;
}

function renderPort(node, port, direction) {
  const element = document.createElement("button");
  element.type = "button";
  element.className = `node-port ${direction}`;
  element.dataset.port = port.name;
  element.title = `${direction === "input" ? "输入" : "输出"}: ${port.name}`;
  element.innerHTML = `<i></i><span>${port.name}</span>`;
  element.addEventListener("pointerdown", (event) => {
    event.stopPropagation();
    if (state.tool !== "connect") return;
    if (direction === "output") {
      state.connectFrom = { nodeID: node.id, port: port.name };
      state.selected = node.id;
      addMessage(`已选择 ${node.id}.${port.name}，请点击目标输入端口`, "assistant");
      render();
      return;
    }
    if (!state.connectFrom) {
      addMessage("请先选择一个输出端口", "error");
      return;
    }
    connectNodes(state.connectFrom.nodeID, state.connectFrom.port, node.id, port.name);
    state.connectFrom = null;
  });
  return element;
}

function startNodeInteraction(event, node) {
  event.stopPropagation();
	state.selectedEdge = null;
  if (state.tool === "connect") {
    if (!state.connectFrom) {
      const output = state.definitions[node.kind]?.outputs?.[0];
      if (!output) {
        addMessage(`${node.id} 没有可用输出端口`, "error");
        return;
      }
      state.connectFrom = { nodeID: node.id, port: output.name };
      state.selected = node.id;
      render();
      addMessage(`已选择 ${node.id}.${output.name}，请点击目标节点完成连线`, "assistant");
      return;
    }
    if (state.connectFrom.nodeID !== node.id) {
      const input = findCompatibleInput(state.connectFrom.nodeID, state.connectFrom.port, node.id);
      if (input) connectNodes(state.connectFrom.nodeID, state.connectFrom.port, node.id, input.name);
      else addMessage("目标节点没有兼容的输入端口", "error");
    }
    state.connectFrom = null;
    return;
  }
	if (state.tool === "pan") return;
  state.selected = node.id;
  showNodeArtifacts(node.id, true);
  const startX = event.clientX;
  const startY = event.clientY;
  const originalX = node.x;
  const originalY = node.y;
  const move = (moveEvent) => {
    node.x = Math.max(8, originalX + (moveEvent.clientX - startX) / state.zoom);
    node.y = Math.max(8, originalY + (moveEvent.clientY - startY) / state.zoom);
    render();
  };
  const stop = () => {
    document.removeEventListener("pointermove", move);
    document.removeEventListener("pointerup", stop);
    recordHistory();
  };
  document.addEventListener("pointermove", move);
  document.addEventListener("pointerup", stop);
  render();
}

function findCompatibleInput(fromNode, fromPort, toNode) {
  const source = state.definitions[state.nodes.find((node) => node.id === fromNode)?.kind];
  const target = state.definitions[state.nodes.find((node) => node.id === toNode)?.kind];
  const output = source?.outputs?.find((port) => port.name === fromPort);
  const inputs = target?.inputs || [];
  return inputs.find((input) => input.artifact_kind === output?.artifact_kind)
    || inputs.find((input) => input.artifact_kind === "resource");
}

function connectNodes(fromNode, fromPort, toNode, toPort) {
  const source = state.definitions[state.nodes.find((node) => node.id === fromNode)?.kind];
  const target = state.definitions[state.nodes.find((node) => node.id === toNode)?.kind];
  const output = source?.outputs?.find((candidate) => candidate.name === fromPort);
  const input = target?.inputs?.find((candidate) => candidate.name === toPort);
  if (!output || !input) {
    addMessage("这两个节点没有可连接的端口", "error");
    return;
  }
  if (input.artifact_kind !== "resource" && input.artifact_kind !== output.artifact_kind) {
    addMessage(`端口类型不匹配：${output.name} 不能连接 ${input.name}`, "error");
    return;
  }
  const exists = state.edges.some((edge) => edge.fromNode === fromNode && edge.toNode === toNode && edge.toPort === input.name);
  if (!exists) {
    state.edges.push({ fromNode, fromPort: output.name, toNode, toPort: input.name });
    recordHistory();
  }
  render();
}

function renderEdges() {
  const svg = $("edges");
  svg.replaceChildren();
  const defs = document.createElementNS("http://www.w3.org/2000/svg", "defs");
  const marker = document.createElementNS("http://www.w3.org/2000/svg", "marker");
  marker.id = "edge-arrow";
  marker.setAttribute("viewBox", "0 0 10 10");
  marker.setAttribute("refX", "9");
  marker.setAttribute("refY", "5");
  marker.setAttribute("markerWidth", "6");
  marker.setAttribute("markerHeight", "6");
  marker.setAttribute("orient", "auto-start-reverse");
  const arrow = document.createElementNS("http://www.w3.org/2000/svg", "path");
  arrow.setAttribute("d", "M 0 0 L 10 5 L 0 10 z");
  arrow.setAttribute("fill", "#63718a");
  marker.appendChild(arrow);
  defs.appendChild(marker);
  svg.appendChild(defs);
  const lineagePairs = artifactLineagePairs();
  state.edges.forEach((edge, index) => {
    const from = state.nodes.find((node) => node.id === edge.fromNode);
    const to = state.nodes.find((node) => node.id === edge.toNode);
    if (!from || !to) return;
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    const x1 = from.x + 250;
    const y1 = portY(from, edge.fromPort, "output");
    const x2 = to.x;
    const y2 = portY(to, edge.toPort, "input");
    const direction = x2 >= x1 ? 1 : -1;
    const bend = Math.min(80, Math.abs(x2 - x1) / 2);
    path.setAttribute("d", `M ${x1} ${y1} C ${x1 + direction * bend} ${y1}, ${x2 - direction * bend} ${y2}, ${x2} ${y2}`);
    path.setAttribute("marker-end", "url(#edge-arrow)");
    const related = !state.selected || edge.fromNode === state.selected || edge.toNode === state.selected;
    path.classList.toggle("active", state.selectedEdge === index);
    path.classList.toggle("lineage", lineagePairs.has(`${from.id}->${to.id}`));
    path.classList.toggle("related", Boolean(state.selected) && related);
    path.classList.toggle("dimmed", Boolean(state.selected) && !related);
    path.addEventListener("pointerdown", (event) => {
      event.stopPropagation();
      state.selected = null;
      state.selectedEdge = index;
      render();
    });
    svg.appendChild(path);
  });
}

function portY(node, portName, direction) {
  const ports = state.definitions[node.kind]?.[direction === "input" ? "inputs" : "outputs"] || [];
  const index = Math.max(0, ports.findIndex((port) => port.name === portName));
  return node.y + 82 + index * 20;
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
  recordHistory();
  render();
}

function deleteSelection() {
  if (state.selectedEdge !== null) {
	state.edges.splice(state.selectedEdge, 1);
	state.selectedEdge = null;
	recordHistory();
	render();
	return true;
  }
  if (!state.selected) return false;
	state.nodes = state.nodes.filter((node) => node.id !== state.selected);
	state.edges = state.edges.filter((edge) => edge.fromNode !== state.selected && edge.toNode !== state.selected);
	state.selected = null;
  recordHistory();
	render();
	return true;
}

async function saveWorkflow() {
  try {
    const operation = await fetchJSON(projectURL("/operations"), {
      method: "POST",
      headers: { "Idempotency-Key": operationKey("workflow") },
      body: JSON.stringify({
        project_id: state.projectID,
        type: "update_workflow",
        payload: workflowPayload(),
      }),
    });
    renderOperation(operation);
    setSaveStatus("待确认");
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
    const operation = await fetchJSON(projectURL("/operations"), {
      method: "POST",
      headers: { "Idempotency-Key": operationKey("run") },
      body: JSON.stringify({ project_id: state.projectID, type: "run", payload }),
    });
    renderOperation(operation);
    setPrimaryAction("pending");
    setStatus("待确认", "running");
  } catch (error) {
    setPrimaryAction("run");
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
      body: JSON.stringify({ project_id: state.projectID, conversation_id: state.conversationID, run_id: state.run?.run_id || "", text: message, product_name: $("product-name").value, product_image_urls: productImageURLs(), brief: $("brief").value }),
    });
    state.conversationID = response.conversation?.conversation_id || state.conversationID;
    if (state.conversationID) localStorage.setItem(storageKey("conversation"), state.conversationID);
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
    const operation = await fetchJSON(projectURL("/operations"), {
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
	const completed = ["applied", "rejected"].includes(operation.status);
	const operationName = operationLabels[operation.type] || operation.type;
	const element = document.createElement("div");
	element.className = "message assistant";
	element.textContent = completed
		? `${operation.status === "applied" ? "操作已确认" : "已返回修改"}：${operationName}`
		: `${operation.status === "confirmed" ? "待继续操作" : "待确认操作"}：${operationName}`;
	$("messages").appendChild(element);

	const card = $("agent-action");
	card.replaceChildren();
	card.hidden = completed;
	if (completed) return;

	const title = document.createElement("div");
	title.className = "agent-action-title";
	title.innerHTML = `<span>▣</span><strong>${operationName}</strong><span class="action-status">${operation.status === "confirmed" ? "已确认" : "待确认"}</span>`;
	const summary = document.createElement("div");
	summary.className = "agent-action-summary";
	summary.textContent = operationPayloadSummary(operation.payload);
	const actions = document.createElement("div");
	actions.className = "agent-action-actions";
	const confirm = document.createElement("button");
	confirm.className = "primary";
	const confirmLabels = { run: "确认并运行", retry: "确认重试", cancel: "确认取消", update_workflow: "确认保存" };
	confirm.textContent = operation.status === "confirmed" ? "继续执行" : confirmLabels[operation.type] || "确认执行";
	const reject = document.createElement("button");
	reject.className = "secondary";
	reject.textContent = "返回修改";
	if (operation.status !== "confirmed") actions.appendChild(reject);
	actions.appendChild(confirm);
	card.append(title, summary, actions);
	if (operation.type === "run") {
		$("task-input").open = false;
	}
	setPrimaryAction("pending");
	requestAnimationFrame(() => card.scrollIntoView({ behavior: "smooth", block: "nearest" }));
	confirm.addEventListener("click", () => confirmOperation(operation, card, element));
	reject.addEventListener("click", async () => {
		try {
			await fetchJSON(`/operations/${operation.operation_id}/reject`, { method: "POST" });
			card.hidden = true;
			element.textContent = "已返回修改";
			if (operation.type === "run") {
				$("task-input").open = true;
				setRunStatus(state.run);
				setPrimaryAction("run");
			} else if (operation.type === "update_workflow") {
				setSaveStatus("未保存");
				setPrimaryAction("save");
			} else {
				syncPrimaryAction();
			}
		} catch (error) {
			addMessage(error.message, "error");
		}
	});
}

function operationPayloadSummary(payload) {
	if (!payload || typeof payload !== "object") return "请确认当前 Agent 建议的操作。";
	const labels = { product_name: "商品", brief: "创作需求", run_id: "运行" };
	return Object.entries(payload)
		.filter(([, value]) => value !== null && value !== "" && !Array.isArray(value) && typeof value !== "object")
		.slice(0, 5)
		.map(([key, value]) => `${labels[key] || key}：${value}`)
		.join("\n") || "请确认当前 Agent 建议的操作。";
}

async function confirmOperation(operation, card, message) {
	try {
		const result = await fetchJSON(`/operations/${operation.operation_id}/confirm`, { method: "POST" });
		card.hidden = true;
		message.textContent = "操作已确认";
		if (!result.run) {
			await loadProjectWorkflow();
			state.dirty = false;
			setSaveStatus("已保存");
			setPrimaryAction("run");
			addMessage("画布已保存，可以开始运行。", "assistant");
			return;
		}
		if (state.timer) clearInterval(state.timer);
		state.run = result.run;
		state.runID = state.run.run_id;
		localStorage.setItem(storageKey("run"), state.runID);
		state.finalAnnouncedRunID = null;
		setPrimaryAction("running");
		updateRun(state.run);
		if (!runFinished(state.run)) state.timer = setInterval(refreshRun, 500);
	} catch (error) {
		addMessage(error.message, "error");
	}
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
  const artifactIDs = new Set();
  state.nodes.forEach((node) => { node.artifacts = []; node.artifactResults = []; });
  run.node_runs.forEach((nodeRun) => {
    const node = nodesByID.get(nodeRun.node_id);
    if (node && !nodeRun.instance_key) {
      node.state = nodeRun.state;
      node.message = nodeRun.message || "";
    }
    (nodeRun.artifacts || []).forEach((artifact) => {
      if (internalArtifactKinds.has(artifact.kind)) return;
      const key = artifactKey(artifact);
      if (artifactIDs.has(key)) return;
      artifactIDs.add(key);
      artifacts.push(artifact);
      if (node) {
        node.artifacts.push(key);
        node.artifactResults.push(artifact);
      }
    });
  });
  state.artifacts = artifacts;
  render();
	if (state.artifactNodeID) showNodeArtifacts(state.artifactNodeID, false);
	else renderArtifacts(artifacts);
	renderRunPanel(run);
	const final = artifacts.find((artifact) => artifact.kind === "finalvideo" && artifact.status === "succeeded");
	const failed = run.node_runs.some((node) => node.state === "failed");
	const retryableFailure = run.node_runs.some((node) => node.state === "failed" && !node.submission_unknown);
	const canceled = run.canceled || run.node_runs.some((node) => node.state === "canceled");
	$("retry-button").disabled = !retryableFailure || canceled;
	$("cancel-button").disabled = runFinished(run);
	setRunStatus(run, final);
  syncPrimaryAction();
  if (final && state.finalAnnouncedRunID !== run.run_id) {
    state.finalAnnouncedRunID = run.run_id;
    addMessage("正式成片已生成，可以在右侧产物区查看。", "assistant");
    focusNode("finalvideo");
  }
}

function renderRunPanel(run) {
	const panel = $("run-inspector");
	if (!run) {
		panel.hidden = true;
		return;
	}
	panel.hidden = false;
	$("run-number").textContent = `Run #${shortRunID(run.run_id)}`;
	const finished = runFinished(run);
	const failed = run.node_runs.some((node) => node.state === "failed");
	const canceled = run.canceled || run.node_runs.some((node) => node.state === "canceled");
	const statusText = canceled ? "已取消" : failed ? "执行失败" : finished ? "已完成" : "运行中";
	setStatusElement($("run-summary-status"), statusText, canceled || failed ? "failed" : finished ? "success" : "running");
	const mainRuns = run.node_runs.filter((node) => !node.instance_key);
	$("run-start-time").textContent = `${mainRuns.length} 个阶段`;

	const steps = $("run-steps");
	steps.replaceChildren();
	mainRuns.forEach((node) => {
		const item = document.createElement("div");
		item.className = `run-step ${node.state}`;
		const icon = node.state === "succeeded" ? "✓" : node.state === "failed" ? "!" : node.state === "running" ? "◌" : "○";
		const children = run.node_runs.filter((child) => child.node_id === node.node_id && child.instance_key);
		const completedChildren = children.filter((child) => child.state === "succeeded").length;
		const progress = children.length ? `${completedChildren}/${children.length} 个结果` : stateLabels[node.state] || node.state;
		item.innerHTML = `<span class="run-step-icon">${icon}</span><span>${nodeLabels[node.node_id] || node.node_id}</span><small>${progress}</small>`;
		steps.appendChild(item);
	});
	const latest = [...run.node_runs].reverse().find((node) => node.message || node.state === "running" || node.state === "succeeded");
	$("run-latest-event").textContent = latest
		? `${nodeLabels[latest.node_id] || latest.node_id} · ${latest.message || stateLabels[latest.state] || latest.state}`
		: "等待节点调度";
	$("run-log").textContent = JSON.stringify(run, null, 2);
}

async function cancelRun() {
  if (!state.run) {
    addMessage("当前没有可取消的运行。", "error");
    return;
  }
  try {
    const operation = await fetchJSON(projectURL("/operations"), {
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

function showNodeArtifacts(nodeID, openPanel) {
  const node = state.nodes.find((candidate) => candidate.id === nodeID);
  if (!node) return;
  state.artifactNodeID = nodeID;
  $("artifact-scope").textContent = nodeLabels[node.id] || nodeLabels[node.kind] || node.id;
  renderArtifacts(node.artifactResults || []);
  if (openPanel && node.artifactResults?.length) $("artifacts-panel").open = true;
}

function focusNode(nodeID) {
  const node = state.nodes.find((candidate) => candidate.id === nodeID);
  if (!node) return;
  state.selected = nodeID;
  showNodeArtifacts(nodeID, true);
  render();
  const canvas = $("canvas");
  canvas.scrollTo({
    left: Math.max(0, node.x * state.zoom - (canvas.clientWidth - 250 * state.zoom) / 2),
    top: Math.max(0, node.y * state.zoom - (canvas.clientHeight - 180 * state.zoom) / 2),
    behavior: "smooth",
  });
}

function setPrimaryAction(action) {
  const button = $("run-button");
  const labels = { run: "运行", save: "保存画布", pending: "等待确认", running: "执行中", final: "查看成片" };
  state.primaryAction = action;
  $("run-primary-label").textContent = labels[action] || labels.run;
  button.disabled = ["pending", "running"].includes(action);
}

function syncPrimaryAction() {
  if (state.dirty) {
    setPrimaryAction("save");
    return;
  }
  if (!state.run) {
    setPrimaryAction("run");
    return;
  }
  const final = state.artifacts.some((artifact) => artifact.kind === "finalvideo" && artifact.status === "succeeded");
  setPrimaryAction(!runFinished(state.run) ? "running" : final ? "final" : "run");
}

function renderArtifacts(artifacts) {
  const container = $("artifacts");
  container.replaceChildren();
  $("artifact-count").textContent = `${artifacts.length} 个`;
  if (!artifacts.length) {
    const empty = document.createElement("div");
    empty.className = "artifact-empty";
    empty.textContent = state.run ? "该节点尚未产生产物" : "运行后，图片、音频和视频会汇总在这里";
    container.appendChild(empty);
    return;
  }
  artifacts.forEach((artifact) => {
    const element = document.createElement("div");
    element.className = "artifact";
    const title = document.createElement("div");
    title.className = "artifact-title";
    const kind = document.createElement("span");
    kind.className = "artifact-kind";
    kind.textContent = artifactLabels[artifact.kind] || artifact.kind;
    const status = document.createElement("span");
    status.className = "artifact-status";
    status.textContent = stateLabels[artifact.status] || artifact.status;
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
  label.textContent = artifactLabels[artifact.kind] || artifact.kind;
  content.appendChild(label);

  const data = artifact.data && typeof artifact.data === "object" ? artifact.data : {};
  const metadata = [
    data.version || artifact.version,
    data.file_name || data.filename || data.name,
    artifact.source || data.source,
  ].filter(Boolean);
  if (metadata.length) {
    const meta = document.createElement("div");
    meta.className = "artifact-meta";
    meta.textContent = metadata.join(" · ");
    content.appendChild(meta);
  }
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

function shortRunID(runID) {
  const parts = String(runID || "").split(/[:-]/).filter(Boolean);
  return parts[parts.length - 1] || "—";
}

function addMessage(text, role) {
  const element = document.createElement("div");
  element.className = `message ${role}`;
  element.textContent = text;
  $("messages").appendChild(element);
  $("messages").scrollTop = $("messages").scrollHeight;
}

function setStatusElement(element, text, className) {
	if (!element) return;
	element.textContent = text;
	element.className = `status-pill ${className || ""}`;
}

function setStatus(text, className) {
  const status = $("run-status");
  setStatusElement(status, text, className);
}

function setRunStatus(run, finalArtifact) {
	if (!run) {
		setStatus("未运行");
		return;
	}
	const failed = run.node_runs.some((node) => node.state === "failed");
	const canceled = run.canceled || run.node_runs.some((node) => node.state === "canceled");
	setStatus(finalArtifact ? "已完成" : canceled ? "已取消" : failed ? "执行失败" : "执行中", finalArtifact ? "success" : canceled || failed ? "failed" : "running");
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
	if (state.tool === "palette") {
		$("node-palette").hidden = !$("node-palette").hidden;
		state.tool = "select";
		button.classList.remove("active");
		document.querySelector('[data-tool="select"]').classList.add("active");
		return;
	}
	render();
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
$("run-button").addEventListener("click", () => {
	if (state.primaryAction === "save") {
		saveWorkflow();
		return;
	}
	if (state.primaryAction === "final") {
		focusNode("finalvideo");
		return;
	}
	runWorkflow();
});
$("undo-button").addEventListener("click", () => restoreHistory(state.historyIndex - 1));
$("redo-button").addEventListener("click", () => restoreHistory(state.historyIndex + 1));
$("zoom-out").addEventListener("click", () => changeZoom(-0.1));
$("zoom-in").addEventListener("click", () => changeZoom(0.1));
$("fit-canvas").addEventListener("click", fitCanvas);
$("fullscreen-button").addEventListener("click", () => document.documentElement.requestFullscreen?.());
$("export-button").addEventListener("click", exportWorkflow);
$("retry-button").addEventListener("click", retryRun);
$("cancel-button").addEventListener("click", cancelRun);
$("send-agent").addEventListener("click", sendAgentMessage);
[$("product-name"), $("product-images"), $("brief")].forEach((input) => input.addEventListener("input", () => {
	if (state.primaryAction === "final") setPrimaryAction("run");
}));
$("view-logs").addEventListener("click", () => {
	$("run-log").hidden = !$("run-log").hidden;
});
$("canvas").addEventListener("dragover", (event) => event.preventDefault());
$("canvas").addEventListener("drop", (event) => {
  event.preventDefault();
  const kind = event.dataTransfer.getData("text/node-kind");
  const bounds = $("canvas").getBoundingClientRect();
  addNode(kind, (event.clientX - bounds.left + $("canvas").scrollLeft) / state.zoom, (event.clientY - bounds.top + $("canvas").scrollTop) / state.zoom);
});
$("canvas").addEventListener("pointerdown", (event) => {
	if (state.tool === "pan") {
		const startX = event.clientX;
		const startY = event.clientY;
		const scrollLeft = $("canvas").scrollLeft;
		const scrollTop = $("canvas").scrollTop;
		const move = (moveEvent) => {
			$("canvas").scrollLeft = scrollLeft - moveEvent.clientX + startX;
			$("canvas").scrollTop = scrollTop - moveEvent.clientY + startY;
		};
		const stop = () => {
			document.removeEventListener("pointermove", move);
			document.removeEventListener("pointerup", stop);
		};
		document.addEventListener("pointermove", move);
		document.addEventListener("pointerup", stop);
		return;
	}
	state.selected = null;
	state.selectedEdge = null;
	state.artifactNodeID = null;
	$("artifact-scope").textContent = "全部";
	renderArtifacts(state.artifacts);
	render();
});
document.addEventListener("keydown", (event) => {
	if (event.key === "Delete" && state.tool === "select") deleteSelection();
});

(async function init() {
  try {
		$("project-id").textContent = `project: ${state.projectID}`;
		await loadRuntime();
		await loadDefinitions();
		await loadProjectWorkflow();
		await restoreSession();
		await restoreConversation();
    await restoreRun();
    $("task-input").open = !state.runID;
    if (!state.run) renderArtifacts([]);
    if (!state.conversationID) addMessage("工作流已加载，可以直接运行或调整节点。", "assistant");
  } catch (error) {
    addMessage(error.message, "error");
  }
})();
