const projectID = new URLSearchParams(window.location.search).get("project_id")?.trim() || "demo";
const storageKey = (name) => `video-agent:${projectID}:${name}`;
const projectURL = (suffix = "") => `/projects/${encodeURIComponent(projectID)}${suffix}`;
const $ = (id) => document.getElementById(id);

const state = {
  projectID,
  definitions: {},
  nodes: [],
  edges: [],
  selected: null,
  selectedEdge: null,
  tool: "select",
  connectFrom: null,
  zoom: 1,
  history: [],
  historyIndex: -1,
  dirty: false,
  run: null,
  runID: localStorage.getItem(storageKey("run")) || "",
  conversationID: localStorage.getItem(storageKey("conversation")) || "",
  artifacts: [],
  artifactNodeID: "",
  timer: null,
  nextNode: 1,
  primaryAction: "run",
  finalAnnouncedRunID: "",
};

const labels = {
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
const nodeLabels = labels;
const artifactLabels = {
  requirement: "需求分析",
  clipscript: "分镜脚本",
  competition_reference_image: "竞品图",
  voice_preview: "TTS 音频",
  character_reference_image: "人物参考图",
  preview_video: "视频预览",
  finalvideo: "正式成片",
};
const operationLabels = { run: "生成视频", retry: "重试运行", cancel: "取消运行", update_workflow: "保存画布" };
const stateLabels = { pending: "等待中", waiting: "等待依赖", running: "生成中", succeeded: "已完成", failed: "执行失败", canceled: "已取消" };
const internalArtifactKinds = new Set(["clipscript_annotation"]);
const paletteGroups = {
  image: new Set(["competition_reference_image", "character_reference_image"]),
  video: new Set(["preview", "finalvideo"]),
  audio: new Set(["prompt_tts"]),
};
const defaultNodes = [
  ["requirement", "requirement", 160, 120],
  ["clipscript", "clipscript", 545, 120],
  ["competition", "competition_reference_image", 250, 420],
  ["tts", "prompt_tts", 625, 420],
  ["character_reference", "character_reference_image", 1000, 420],
  ["preview", "preview", 625, 720],
  ["finalvideo", "finalvideo", 1000, 720],
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

function nodeLabel(node) { return labels[node.id] || labels[node.kind] || node.id; }
function artifactKey(artifact) { return artifact.artifact_id || artifact.id || ""; }
function clone(value) { return JSON.parse(JSON.stringify(value)); }
function shortRunID(value) { return String(value || "").split(/[:-]/).filter(Boolean).pop() || "—"; }

async function fetchJSON(url, options = {}) {
  const response = await fetch(url, { ...options, headers: { "Content-Type": "application/json", ...(options.headers || {}) } });
  if (!response.ok) {
    let message = `请求失败: ${response.status}`;
    try { message = (await response.json()).error || message; } catch (_) { /* 继续使用状态码。 */ }
    throw new Error(message);
  }
  return response.json();
}

async function loadRuntime() {
  try {
    const runtime = await fetchJSON("/runtime");
    const enabled = [runtime.image && "图片", runtime.tts && "TTS", runtime.preview && "预览", runtime.finalvideo && "成片"].filter(Boolean);
    $("runtime-mode").textContent = runtime.mode === "remote" ? "远端运行" : "本地模拟";
    $("runtime-mode").title = enabled.length ? `已装配：${enabled.join("、")}` : "未装配媒体能力";
  } catch (_) { $("runtime-mode").textContent = "运行状态未知"; }
}

async function loadDefinitions() {
  state.definitions = await fetchJSON("/workflow/node-definitions");
  const palette = $("node-palette");
  palette.replaceChildren();
  Object.keys(state.definitions).forEach((kind) => {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "palette-node";
    item.draggable = true;
    item.dataset.kind = kind;
    item.textContent = nodeLabels[kind] || kind;
    item.addEventListener("dragstart", (event) => event.dataTransfer.setData("text/node-kind", kind));
    item.addEventListener("click", () => { addNode(kind, 240 + (state.nodes.length % 3) * 350, 160 + Math.floor(state.nodes.length / 3) * 250); palette.hidden = true; });
    palette.appendChild(item);
  });
}

async function loadProject() {
  try {
    const project = await fetchJSON(projectURL());
    if (project.name) { $("project-name").textContent = project.name; $("canvas-project-label").textContent = project.name; }
    const versions = project.workflow_versions || [];
    const version = versions.find((item) => item.workflow_version_id === project.current_workflow_version) || versions.at(-1);
    if (version) {
      state.nodes = (version.nodes || []).map((node, index) => ({ id: node.node_id, kind: node.kind, config: node.config, x: node.layout?.x > 0 ? node.layout.x : defaultNodes.find((item) => item.id === node.node_id)?.x ?? 120 + index % 3 * 350, y: node.layout?.y > 0 ? node.layout.y : defaultNodes.find((item) => item.id === node.node_id)?.y ?? 120 + Math.floor(index / 3) * 260 }));
      state.edges = (version.edges || []).map((edge) => ({ fromNode: edge.from_node, fromPort: edge.from_port, toNode: edge.to_node, toPort: edge.to_port }));
      resetHistory();
      render();
      return;
    }
  } catch (error) {
    if (!error.message.includes("project not found")) throw error;
  }
  resetWorkflow(false);
}

async function restoreSession() {
  const session = await fetchJSON(projectURL("/session"));
  state.conversationID = session.conversation?.conversation_id || state.conversationID;
  state.runID = session.run_id || state.runID;
  persistSession();
}

function persistSession() {
  if (state.conversationID) localStorage.setItem(storageKey("conversation"), state.conversationID); else localStorage.removeItem(storageKey("conversation"));
  if (state.runID) localStorage.setItem(storageKey("run"), state.runID); else localStorage.removeItem(storageKey("run"));
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
  persistSession();
}

async function restoreRun() {
  if (!state.runID) return;
  try {
    state.run = await fetchJSON(`/runs/${state.runID}`);
    updateRun(state.run);
    if (!runFinished(state.run)) state.timer = setInterval(refreshRun, 700);
  } catch (_) {
    state.run = null; state.runID = ""; persistSession();
  }
}

function resetWorkflow(changed = true) {
  state.nodes = defaultNodes.map((node) => ({ ...node }));
  state.edges = defaultEdges.map((edge) => ({ ...edge }));
  state.selected = null; state.selectedEdge = null; state.connectFrom = null;
  state.artifacts = []; state.artifactNodeID = "";
  resetHistory();
  if (changed) markDirty();
  render();
}

function snapshot() {
  return JSON.stringify({ nodes: state.nodes.map(({ id, kind, config, x, y }) => ({ id, kind, config, x, y })), edges: state.edges });
}
function resetHistory() { state.history = [snapshot()]; state.historyIndex = 0; state.dirty = false; setSaveStatus("已保存"); updateHistoryButtons(); }
function recordHistory() {
  const value = snapshot();
  if (state.history[state.historyIndex] === value) return;
  state.history = state.history.slice(0, state.historyIndex + 1); state.history.push(value); state.historyIndex++;
  markDirty(); updateHistoryButtons();
}
function restoreHistory(index) {
  const value = state.history[index];
  if (!value) return;
  const workflow = JSON.parse(value);
  state.nodes = workflow.nodes || []; state.edges = workflow.edges || []; state.historyIndex = index;
  state.selected = null; state.selectedEdge = null; state.connectFrom = null; markDirty(); updateHistoryButtons(); render();
}
function updateHistoryButtons() {
  const undo = state.historyIndex > 0; const redo = state.historyIndex >= 0 && state.historyIndex < state.history.length - 1;
  $("undo-button").disabled = !undo; $("redo-button").disabled = !redo;
  document.querySelector('[data-history="undo"]').disabled = !undo; document.querySelector('[data-history="redo"]').disabled = !redo;
}
function markDirty() { state.dirty = true; setSaveStatus("未保存"); if (!state.run || runFinished(state.run)) setPrimaryAction("save"); }
function setSaveStatus(value) { $("save-status").textContent = value === "已保存" ? "● 已保存" : `○ ${value}`; }

function applyZoom(value) { state.zoom = Math.min(1.5, Math.max(.65, value)); $("canvas-stage").style.transform = `scale(${state.zoom})`; $("zoom-value").textContent = `${Math.round(state.zoom * 100)}%`; renderEdges(); }
function fitCanvas() { applyZoom(1); $("canvas").scrollTo({ left: 0, top: 0 }); }
function exportWorkflow() {
  const blob = new Blob([JSON.stringify(workflowPayload(), null, 2)], { type: "application/json" });
  const link = document.createElement("a"); link.href = URL.createObjectURL(blob); link.download = `${$("project-name").textContent || "video-agent"}.workflow.json`; link.click(); URL.revokeObjectURL(link.href);
}

function render() {
  const nodes = $("nodes"); nodes.replaceChildren(); $("canvas").classList.toggle("connect-mode", state.tool === "connect");
  state.nodes.forEach((node) => nodes.appendChild(renderNode(node)));
  renderEdges();
}
function renderNode(node) {
  const element = document.createElement("article");
  element.className = `node ${node.state || ""} ${state.selected === node.id ? "selected" : ""}`; element.dataset.nodeId = node.id; element.style.left = `${node.x}px`; element.style.top = `${node.y}px`;
  const head = document.createElement("div"); head.className = "node-head";
  const dot = document.createElement("span"); dot.className = "node-dot";
  const title = document.createElement("strong"); title.textContent = nodeLabel(node); const code = document.createElement("small"); code.className = "node-code"; code.textContent = node.kind; title.appendChild(code);
  const status = document.createElement("span"); status.className = "node-state"; status.textContent = stateLabels[node.state] || "待运行"; head.append(dot, title, status); element.append(head, renderPorts(node));
  const content = document.createElement("div"); content.className = "node-content"; renderNodeContent(content, node); element.append(content);
  if (node.message && ["running", "failed"].includes(node.state)) { const message = document.createElement("div"); message.className = "node-message"; message.textContent = node.message; element.appendChild(message); }
  element.addEventListener("pointerdown", (event) => startNodeInteraction(event, node));
  return element;
}
function renderNodeContent(container, node) {
  const artifacts = node.artifactResults || [];
  if (artifacts.length) {
    if (artifacts.some((artifact) => artifact.kind === "requirement")) element.classList.add("has-requirement-result");
    const visible = ["requirement", "clipscript"].includes(node.kind) ? artifacts : artifacts.slice(0, 1);
    visible.forEach((artifact) => container.appendChild(createArtifactContent(artifact, true)));
    if (artifacts.length > visible.length) { const count = document.createElement("div"); count.className = "node-result-count"; count.textContent = `还有 ${artifacts.length - visible.length} 个产物`; container.appendChild(count); }
    return;
  }
  if (node.config?.text) { const text = document.createElement("div"); text.className = "node-text"; text.textContent = node.config.text; container.appendChild(text); return; }
  const placeholder = document.createElement("div"); placeholder.className = "node-placeholder";
  if (["competition_reference_image", "character_reference_image"].includes(node.kind)) { placeholder.classList.add("image"); placeholder.textContent = "图片结果"; }
  else if (node.kind === "prompt_tts") { placeholder.classList.add("audio"); placeholder.textContent = "音频结果"; }
  else if (["preview", "finalvideo"].includes(node.kind)) { placeholder.classList.add("video"); placeholder.textContent = "视频结果"; }
  else { placeholder.textContent = node.kind === "requirement" || node.kind === "clipscript" ? "运行后显示模型结果" : "等待上游内容"; }
  container.appendChild(placeholder);
}
function renderPorts(node) {
  const definition = state.definitions[node.kind] || {}; const ports = document.createElement("div"); ports.className = "node-ports";
  const inputs = document.createElement("div"); inputs.className = "node-port-group input-group"; const outputs = document.createElement("div"); outputs.className = "node-port-group output-group";
  (definition.inputs || []).forEach((port) => inputs.appendChild(renderPort(node, port, "input"))); (definition.outputs || []).forEach((port) => outputs.appendChild(renderPort(node, port, "output"))); ports.append(inputs, outputs); return ports;
}
function renderPort(node, port, direction) {
  const button = document.createElement("button"); button.type = "button"; button.className = `node-port ${direction}`; button.title = `${direction === "input" ? "输入" : "输出"}: ${port.name}`; button.innerHTML = `<i></i><span>${port.name}</span>`;
  button.addEventListener("pointerdown", (event) => {
    event.stopPropagation();
    if (state.tool !== "connect") return;
    if (direction === "output") { state.connectFrom = { nodeID: node.id, port: port.name }; state.selected = node.id; render(); addMessage(`已选择 ${nodeLabel(node)} 的 ${port.name}，请点击目标输入`, "assistant"); return; }
    if (!state.connectFrom) { addMessage("请先点击一个输出端口", "error"); return; }
    connectNodes(state.connectFrom.nodeID, state.connectFrom.port, node.id, port.name); state.connectFrom = null;
  });
  return button;
}
function startNodeInteraction(event, node) {
  event.stopPropagation(); state.selectedEdge = null;
  if (state.tool === "connect") {
    if (!state.connectFrom) {
      const output = state.definitions[node.kind]?.outputs?.[0]; if (!output) { addMessage(`${nodeLabel(node)} 没有输出端口`, "error"); return; }
      state.connectFrom = { nodeID: node.id, port: output.name }; state.selected = node.id; render(); addMessage(`已选择 ${nodeLabel(node)}，请点击目标节点`, "assistant"); return;
    }
    if (state.connectFrom.nodeID !== node.id) { const input = findCompatibleInput(state.connectFrom.nodeID, state.connectFrom.port, node.id); if (input) connectNodes(state.connectFrom.nodeID, state.connectFrom.port, node.id, input.name); else addMessage("目标节点没有兼容的输入端口", "error"); }
    state.connectFrom = null; return;
  }
  if (state.tool === "pan") return;
  state.selected = node.id; showNodeArtifacts(node.id, true);
  const startX = event.clientX, startY = event.clientY, originalX = node.x, originalY = node.y;
  const move = (moveEvent) => { node.x = Math.max(8, originalX + (moveEvent.clientX - startX) / state.zoom); node.y = Math.max(8, originalY + (moveEvent.clientY - startY) / state.zoom); render(); };
  const stop = () => { document.removeEventListener("pointermove", move); document.removeEventListener("pointerup", stop); recordHistory(); };
  document.addEventListener("pointermove", move); document.addEventListener("pointerup", stop); render();
}
function findCompatibleInput(fromNode, fromPort, toNode) {
  const source = state.definitions[state.nodes.find((node) => node.id === fromNode)?.kind]; const target = state.definitions[state.nodes.find((node) => node.id === toNode)?.kind];
  const output = source?.outputs?.find((port) => port.name === fromPort); return (target?.inputs || []).find((port) => port.artifact_kind === output?.artifact_kind) || (target?.inputs || []).find((port) => port.artifact_kind === "resource");
}
function connectNodes(fromNode, fromPort, toNode, toPort) {
  const source = state.definitions[state.nodes.find((node) => node.id === fromNode)?.kind]; const target = state.definitions[state.nodes.find((node) => node.id === toNode)?.kind];
  const output = source?.outputs?.find((port) => port.name === fromPort); const input = target?.inputs?.find((port) => port.name === toPort);
  if (!output || !input) { addMessage("这两个节点没有可连接的端口", "error"); return; }
  if (input.artifact_kind !== "resource" && input.artifact_kind !== output.artifact_kind) { addMessage(`端口类型不匹配：${output.name} 不能连接 ${input.name}`, "error"); return; }
  if (!state.edges.some((edge) => edge.fromNode === fromNode && edge.toNode === toNode && edge.toPort === toPort)) { state.edges.push({ fromNode, fromPort, toNode, toPort }); recordHistory(); }
  render();
}
function renderEdges() {
  const svg = $("edges"); svg.replaceChildren();
  const defs = document.createElementNS("http://www.w3.org/2000/svg", "defs"); const marker = document.createElementNS("http://www.w3.org/2000/svg", "marker"); marker.id = "edge-arrow"; marker.setAttribute("viewBox", "0 0 10 10"); marker.setAttribute("refX", "9"); marker.setAttribute("refY", "5"); marker.setAttribute("markerWidth", "6"); marker.setAttribute("markerHeight", "6"); marker.setAttribute("orient", "auto-start-reverse");
  const arrow = document.createElementNS("http://www.w3.org/2000/svg", "path"); arrow.setAttribute("d", "M 0 0 L 10 5 L 0 10 z"); arrow.setAttribute("fill", "#aeb8c8"); marker.appendChild(arrow); defs.appendChild(marker); svg.appendChild(defs);
  const lineage = artifactLineagePairs();
  state.edges.forEach((edge, index) => {
    const from = state.nodes.find((node) => node.id === edge.fromNode); const to = state.nodes.find((node) => node.id === edge.toNode); if (!from || !to) return;
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path"); const x1 = from.x + 310, y1 = portY(from, edge.fromPort, "output"), x2 = to.x, y2 = portY(to, edge.toPort, "input"); const direction = x2 >= x1 ? 1 : -1; const bend = Math.min(80, Math.abs(x2 - x1) / 2);
    path.setAttribute("d", `M ${x1} ${y1} C ${x1 + direction * bend} ${y1}, ${x2 - direction * bend} ${y2}, ${x2} ${y2}`); path.setAttribute("marker-end", "url(#edge-arrow)");
    const related = !state.selected || edge.fromNode === state.selected || edge.toNode === state.selected; path.classList.toggle("active", state.selectedEdge === index); path.classList.toggle("lineage", lineage.has(`${from.id}->${to.id}`)); path.classList.toggle("related", Boolean(state.selected) && related); path.classList.toggle("dimmed", Boolean(state.selected) && !related);
    path.addEventListener("pointerdown", (event) => { event.stopPropagation(); state.selected = null; state.selectedEdge = index; render(); }); svg.appendChild(path);
  });
}
function portY(node, name, direction) { const ports = state.definitions[node.kind]?.[direction === "input" ? "inputs" : "outputs"] || []; return node.y + 59 + Math.max(0, ports.findIndex((port) => port.name === name)) * 19; }
function artifactLineagePairs() {
  const owners = new Map(); state.artifacts.forEach((artifact) => state.nodes.find((node) => (node.artifacts || []).includes(artifactKey(artifact))) && owners.set(artifactKey(artifact), state.nodes.find((node) => (node.artifacts || []).includes(artifactKey(artifact)))));
  const pairs = new Set(); state.artifacts.forEach((artifact) => { const target = owners.get(artifactKey(artifact)); (artifact.parent_ids || []).forEach((parent) => { const source = owners.get(parent); if (source && target && source.id !== target.id) pairs.add(`${source.id}->${target.id}`); }); }); return pairs;
}
function workflowPayload() { return { nodes: state.nodes.map(({ id, kind, config, x, y }) => ({ node_id: id, kind, config, layout: { x, y } })), edges: state.edges.map(({ fromNode, fromPort, toNode, toPort }) => ({ from_node: fromNode, from_port: fromPort, to_node: toNode, to_port: toPort })) }; }
function productImageURLs() { return $("product-images").value.split(/\s*[\n,]\s*/).map((value) => value.trim()).filter(Boolean); }
function addNode(kind, x, y) { if (!state.definitions[kind]) return; const id = `${kind}-${state.nextNode++}`; state.nodes.push({ id, kind, x: Math.max(8, x), y: Math.max(8, y) }); state.selected = id; state.selectedEdge = null; recordHistory(); render(); }
function deleteSelection() {
  if (state.selectedEdge !== null) { state.edges.splice(state.selectedEdge, 1); state.selectedEdge = null; recordHistory(); render(); return true; }
  if (!state.selected) return false; const id = state.selected; state.nodes = state.nodes.filter((node) => node.id !== id); state.edges = state.edges.filter((edge) => edge.fromNode !== id && edge.toNode !== id); state.selected = null; recordHistory(); render(); return true;
}

async function saveWorkflow() {
  try {
    const operation = await fetchJSON(projectURL("/operations"), { method: "POST", headers: { "Idempotency-Key": operationKey("workflow") }, body: JSON.stringify({ type: "update_workflow", payload: workflowPayload() }) });
    renderOperation(operation); setSaveStatus("待确认"); addMessage("画布修改已提交，请在助手中确认保存。", "assistant");
  } catch (error) { addMessage(error.message, "error"); }
}
async function runWorkflow() {
  const brief = $("brief").value.trim(); addMessage(brief || "开始生成视频", "user");
  try {
    const operation = await fetchJSON(projectURL("/operations"), { method: "POST", headers: { "Idempotency-Key": operationKey("run") }, body: JSON.stringify({ type: "run", payload: { product_name: $("product-name").value, product_image_urls: productImageURLs(), brief } }) });
    renderOperation(operation); setPrimaryAction("pending"); setStatus("待确认", "running");
  } catch (error) { addMessage(error.message, "error"); }
}
async function sendAgentMessage() {
  const text = $("agent-message").value.trim(); if (!text) return; addMessage(text, "user"); $("agent-message").value = "";
  try {
    const response = await fetchJSON("/agent/chat", { method: "POST", headers: { "Idempotency-Key": operationKey("chat") }, body: JSON.stringify({ project_id: state.projectID, conversation_id: state.conversationID, run_id: state.run?.run_id || "", text, product_name: $("product-name").value, product_image_urls: productImageURLs(), brief: $("brief").value }) });
    state.conversationID = response.conversation?.conversation_id || state.conversationID; persistSession();
    const part = response.messages?.at(-1)?.parts?.find((item) => item.type === "text"); addMessage(part?.text || "Agent 已处理请求。", "assistant"); if (response.operation) renderOperation(response.operation);
  } catch (error) { addMessage(error.message, "error"); }
}
async function retryRun() {
  if (!state.run) { addMessage("当前没有可重试的运行。", "error"); return; }
  try { const operation = await fetchJSON(projectURL("/operations"), { method: "POST", headers: { "Idempotency-Key": operationKey(`retry:${state.run.run_id}`) }, body: JSON.stringify({ type: "retry", run_id: state.run.run_id, payload: { run_id: state.run.run_id } }) }); renderOperation(operation); } catch (error) { addMessage(error.message, "error"); }
}
async function cancelRun() {
  if (!state.run) { addMessage("当前没有可取消的运行。", "error"); return; }
  try { const operation = await fetchJSON(projectURL("/operations"), { method: "POST", headers: { "Idempotency-Key": operationKey(`cancel:${state.run.run_id}`) }, body: JSON.stringify({ type: "cancel", run_id: state.run.run_id, payload: { run_id: state.run.run_id } }) }); renderOperation(operation); } catch (error) { addMessage(error.message, "error"); }
}

function renderOperation(operation) {
  /* 已确认但尚未应用的操作仍通过 confirmOperation(operation, card, element) 恢复执行。 */
  const card = $("agent-action"); card.replaceChildren(); card.hidden = false;
  const title = document.createElement("div"); title.className = "agent-action-title"; const actionStatus = operation.status === "confirmed" ? "待继续操作" : "待确认"; title.innerHTML = `<span>▣</span><strong>${operationLabels[operation.type] || operation.type}</strong><span class="action-status">${actionStatus}</span>`;
  const summary = document.createElement("div"); summary.className = "agent-action-summary"; summary.textContent = operationSummary(operation.payload);
  const actions = document.createElement("div"); actions.className = "agent-action-actions"; const confirm = document.createElement("button"); confirm.className = "primary"; confirm.textContent = operation.status === "confirmed" ? "继续执行" : ({ run: "确认并运行", retry: "确认重试", cancel: "确认取消", update_workflow: "确认保存" }[operation.type] || "确认执行");
  if (["applied", "rejected"].includes(operation.status)) { card.hidden = true; return; }
  if (operation.status !== "confirmed") { const reject = document.createElement("button"); reject.className = "secondary"; reject.textContent = "返回修改"; reject.addEventListener("click", () => rejectOperation(operation, card)); actions.appendChild(reject); }
  actions.appendChild(confirm); card.append(title, summary, actions); card.scrollIntoView({ behavior: "smooth", block: "nearest" }); confirm.addEventListener("click", () => confirmOperation(operation, card, null));
}
function operationSummary(payload) {
  if (!payload || typeof payload !== "object") return "请确认当前助手建议的操作。";
  const names = { product_name: "商品", brief: "需求", run_id: "运行" };
  return Object.entries(payload).filter(([, value]) => value !== null && value !== "" && !Array.isArray(value) && typeof value !== "object").slice(0, 5).map(([key, value]) => `${names[key] || key}：${value}`).join("\n") || "请确认当前助手建议的操作。";
}
async function rejectOperation(operation, card) {
  try { await fetchJSON(`/operations/${operation.operation_id}/reject`, { method: "POST" }); const element = card; element.textContent = "已返回修改"; card.hidden = true; addMessage("已返回修改，你可以继续调整需求或画布。", "assistant"); if (operation.type === "run") { setPrimaryAction("run"); if (state.run) setRunStatus(state.run); } } catch (error) { addMessage(error.message, "error"); }
}
async function confirmOperation(operation, card, message) {
  try {
    const result = await fetchJSON(`/operations/${operation.operation_id}/confirm`, { method: "POST" }); card.hidden = true;
    if (!result.run) { await loadProject(); setSaveStatus("已保存"); setPrimaryAction("run"); addMessage("画布已保存，可以开始运行。", "assistant"); return; }
    if (state.timer) clearInterval(state.timer); state.run = result.run; state.runID = state.run.run_id; state.finalAnnouncedRunID = ""; persistSession(); setPrimaryAction("running"); updateRun(state.run); if (!runFinished(state.run)) state.timer = setInterval(refreshRun, 700);
  } catch (error) { addMessage(error.message, "error"); }
}
async function refreshRun() { if (!state.run) return; try { state.run = await fetchJSON(`/runs/${state.run.run_id}`); updateRun(state.run); if (runFinished(state.run)) clearInterval(state.timer); } catch (error) { clearInterval(state.timer); addMessage(error.message, "error"); } }
function runFinished(run) { return Boolean(run?.canceled) || (run?.node_runs || []).length > 0 && run.node_runs.every((node) => ["succeeded", "failed", "canceled"].includes(node.state)); }
function updateRun(run) {
  const nodes = new Map(state.nodes.map((node) => [node.id, node])); const artifacts = []; const seen = new Set(); state.nodes.forEach((node) => { node.artifacts = []; node.artifactResults = []; });
  (run.node_runs || []).forEach((nodeRun) => {
    const node = nodes.get(nodeRun.node_id); if (node && !nodeRun.instance_key) { node.state = nodeRun.state; node.message = nodeRun.message || ""; }
    (nodeRun.artifacts || []).forEach((artifact) => { if (internalArtifactKinds.has(artifact.kind) || seen.has(artifactKey(artifact))) return; seen.add(artifactKey(artifact)); artifacts.push(artifact); if (node) { node.artifacts.push(artifactKey(artifact)); node.artifactResults.push(artifact); } });
  });
  state.artifacts = artifacts; render(); if (state.artifactNodeID) showNodeArtifacts(state.artifactNodeID, false); else renderArtifacts(artifacts); renderRunPanel(run);
  const final = artifacts.find((artifact) => artifact.kind === "finalvideo" && artifact.status === "succeeded"); const failed = (run.node_runs || []).some((node) => node.state === "failed"); const retryable = (run.node_runs || []).some((node) => node.state === "failed" && !node.submission_unknown); const canceled = run.canceled || (run.node_runs || []).some((node) => node.state === "canceled");
  $("retry-button").disabled = !retryable || canceled; $("cancel-button").disabled = runFinished(run); setRunStatus(run, final); syncPrimaryAction();
  if (final && state.finalAnnouncedRunID !== run.run_id) { state.finalAnnouncedRunID = run.run_id; addMessage("正式成片已生成，可以在产物区查看。", "assistant"); focusNode("finalvideo"); }
}
function renderRunPanel(run) {
  const panel = $("run-inspector"); panel.hidden = !run; if (!run) return; $("run-number").textContent = `Run #${shortRunID(run.run_id)}`; const failed = run.node_runs.some((node) => node.state === "failed"); const canceled = run.canceled || run.node_runs.some((node) => node.state === "canceled"); const finished = runFinished(run); setStatusElement($("run-summary-status"), canceled ? "已取消" : failed ? "执行失败" : finished ? "已完成" : "运行中", canceled || failed ? "failed" : finished ? "success" : "running"); $("run-start-time").textContent = `${run.node_runs.filter((node) => !node.instance_key).length} 个阶段`;
  const steps = $("run-steps"); steps.replaceChildren(); run.node_runs.filter((node) => !node.instance_key).forEach((node) => { const item = document.createElement("div"); item.className = `run-step ${node.state}`; item.innerHTML = `<span class="run-step-icon">${node.state === "succeeded" ? "✓" : node.state === "failed" ? "!" : node.state === "running" ? "◌" : "○"}</span><span>${labels[node.node_id] || node.node_id}</span><small>${stateLabels[node.state] || node.state}</small>`; steps.appendChild(item); });
  const latest = [...run.node_runs].reverse().find((node) => node.message || node.state === "running" || node.state === "succeeded"); $("run-latest-event").textContent = latest ? `${labels[latest.node_id] || latest.node_id} · ${latest.message || stateLabels[latest.state] || latest.state}` : "等待节点调度"; $("run-log").textContent = JSON.stringify(run, null, 2);
}

function showNodeArtifacts(nodeID, open) { const node = state.nodes.find((item) => item.id === nodeID); if (!node) return; state.artifactNodeID = nodeID; $("artifact-scope").textContent = nodeLabel(node); renderArtifacts(node.artifactResults || []); if (open && node.artifactResults?.length) $("artifacts-panel").open = true; }
function focusNode(nodeID) { const node = state.nodes.find((item) => item.id === nodeID); if (!node) return; state.selected = nodeID; showNodeArtifacts(nodeID, true); render(); $("canvas").scrollTo({ left: Math.max(0, node.x * state.zoom - 230), top: Math.max(0, node.y * state.zoom - 160), behavior: "smooth" }); }
function renderArtifacts(artifacts) { const container = $("artifacts"); container.replaceChildren(); $("artifact-count").textContent = `${artifacts.length} 个`; if (!artifacts.length) { const empty = document.createElement("div"); empty.className = "artifact-empty"; empty.textContent = state.run ? "该节点尚未产生产物" : "运行后，结果会显示在这里"; container.appendChild(empty); return; } artifacts.forEach((artifact) => { const item = document.createElement("div"); item.className = "artifact"; const title = document.createElement("div"); title.className = "artifact-title"; title.innerHTML = `<span class="artifact-kind">${artifactLabels[artifact.kind] || artifact.kind}</span><span class="artifact-status">${stateLabels[artifact.status] || artifact.status}</span>`; item.appendChild(title); if (artifact.parent_ids?.length) { const parents = document.createElement("div"); parents.className = "artifact-parents"; parents.textContent = `来源：${artifact.parent_ids.join("、")}`; item.appendChild(parents); } item.appendChild(createArtifactContent(artifact, false)); container.appendChild(item); }); }
function createArtifactContent(artifact, compact) {
  const content = document.createElement("div"); content.className = `artifact-content ${compact ? "compact" : ""}`; const data = artifact.data && typeof artifact.data === "object" ? artifact.data : {};
  if (artifact.kind === "requirement") { const label = document.createElement("div"); label.className = "artifact-content-label"; label.textContent = "模型需求分析"; content.classList.add("requirement-result"); content.appendChild(label); appendArtifactText(content, data.markdown || data.objective || ""); if (data.audience) appendArtifactText(content, `受众：${data.audience}`); if (data.selling_points?.length) appendArtifactText(content, `卖点：${data.selling_points.join("、")}`); return content; }
  if (artifact.kind === "clipscript") { appendArtifactText(content, data.title || "分镜脚本"); const scenes = document.createElement("ol"); scenes.className = "artifact-scenes"; (data.scenes || []).slice(0, compact ? 2 : 20).forEach((scene) => { const item = document.createElement("li"); item.textContent = [scene.visual, scene.voiceover].filter(Boolean).join(" / "); scenes.appendChild(item); }); if (scenes.childElementCount) content.appendChild(scenes); return content; }
  if (["competition_reference_image", "character_reference_image"].includes(artifact.kind)) { appendArtifactMedia(content, "img", data.url, data.uri, "图片结果"); return content; }
  if (artifact.kind === "voice_preview") { appendArtifactMedia(content, "audio", data.preview_audio_url || data.example_audio_url || data.audio_url, data.preview_audio_uri || data.example_audio_uri || data.audio_uri, "音频结果"); return content; }
  if (["preview_video", "finalvideo"].includes(artifact.kind)) { appendArtifactMedia(content, "video", data.url || data.preview_video_url || data.finalvideo_url, data.uri, "视频结果"); return content; }
  appendArtifactText(content, artifact.message || artifactDataSummary(data)); return content;
}
function appendArtifactText(container, text) { if (!text) return; const element = document.createElement("div"); element.className = "artifact-text"; element.textContent = text; container.appendChild(element); }
function appendArtifactMedia(container, type, rawURL, uri, label) { const url = safeArtifactURL(rawURL); if (!url || new URL(url).hostname === "local") { const placeholder = document.createElement("div"); placeholder.className = `artifact-media-placeholder ${type}`; placeholder.textContent = `${label}（本地模拟）`; if (uri || rawURL) placeholder.title = uri || rawURL; container.appendChild(placeholder); return; } const media = document.createElement(type); media.className = "artifact-media"; media.src = url; if (type === "img") { media.alt = label; media.loading = "lazy"; } else { media.controls = true; media.preload = "metadata"; media.playsInline = true; } media.addEventListener("error", () => { const fallback = document.createElement("div"); fallback.className = `artifact-media-placeholder ${type}`; fallback.textContent = `${label}加载失败`; media.replaceWith(fallback); }); container.appendChild(media); }
function artifactDataSummary(data) { return Object.entries(data || {}).filter(([, value]) => value !== null && value !== "" && !Array.isArray(value) && typeof value !== "object").slice(0, 4).map(([key, value]) => `${key}: ${value}`).join("\n"); }
function addMessage(text, role) {
  if (!text) return;
  const row = document.createElement("div"); row.className = `message-row ${role}`;
  if (role === "assistant") { const avatar = document.createElement("span"); avatar.className = "message-avatar"; avatar.textContent = "✦"; row.appendChild(avatar); }
  const message = document.createElement("div"); message.className = `message ${role}`; message.textContent = text;
  const time = document.createElement("small"); time.className = "message-time"; time.textContent = new Date().toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  message.appendChild(time); row.appendChild(message); $("messages").appendChild(row); $("messages").scrollTop = $("messages").scrollHeight;
}
function setStatusElement(element, text, className = "") { element.textContent = text; element.className = `status-pill ${className}`; }
function setStatus(text, className) { setStatusElement($("run-status"), text, className); }
function setRunStatus(run, final) { if (!run) { setStatus("未运行"); return; } const failed = run.node_runs.some((node) => node.state === "failed"); const canceled = run.canceled || run.node_runs.some((node) => node.state === "canceled"); setStatus(final ? "已完成" : canceled ? "已取消" : failed ? "执行失败" : "执行中", final ? "success" : canceled || failed ? "failed" : "running"); }
function setPrimaryAction(action) { state.primaryAction = action; $("run-primary-label").textContent = { run: "运行", save: "保存画布", pending: "等待确认", running: "执行中", final: "查看成片" }[action] || "运行"; $("run-button").disabled = ["pending", "running"].includes(action); }
function syncPrimaryAction() { if (state.dirty) { setPrimaryAction("save"); return; } if (!state.run) { setPrimaryAction("run"); return; } const final = state.artifacts.some((artifact) => artifact.kind === "finalvideo" && artifact.status === "succeeded"); setPrimaryAction(!runFinished(state.run) ? "running" : final ? "final" : "run"); }
function operationKey(action) { return `${action}:${crypto.randomUUID()}`; }
function safeArtifactURL(value) { if (!value) return ""; try { const url = new URL(value, window.location.origin); return ["http:", "https:"].includes(url.protocol) ? url.href : ""; } catch (_) { return ""; } }

document.querySelectorAll("[data-tool]").forEach((button) => button.addEventListener("click", () => {
  const tool = button.dataset.tool; const palette = $("node-palette");
  if (tool === "palette") { palette.hidden = !palette.hidden; state.tool = "select"; render(); return; }
  if (paletteGroups[tool]) { palette.hidden = false; palette.querySelectorAll(".palette-node").forEach((item) => { item.hidden = !paletteGroups[tool].has(item.dataset.kind); }); state.tool = "select"; render(); return; }
  if (tool === "editor") { addMessage("视频编辑器入口已保留，选择视频产物后可继续编辑。", "assistant"); return; }
  if (tool === "group") { addMessage("请选择多个节点后再使用分组。", "assistant"); return; }
  state.tool = tool; state.connectFrom = null; document.querySelectorAll(".canvas-view-rail button").forEach((item) => item.classList.toggle("active", item.dataset.tool === tool)); render();
}));
document.querySelectorAll("[data-history]").forEach((button) => button.addEventListener("click", () => restoreHistory(state.historyIndex + (button.dataset.history === "undo" ? -1 : 1))));
$("save-workflow").addEventListener("click", saveWorkflow); $("reset-workflow").addEventListener("click", () => resetWorkflow()); $("reset-input").addEventListener("click", () => { $("product-name").value = ""; $("product-images").value = ""; $("brief").value = ""; });
$("run-button").addEventListener("click", () => { if (state.primaryAction === "save") return saveWorkflow(); if (state.primaryAction === "final") return focusNode("finalvideo"); return runWorkflow(); });
$("undo-button").addEventListener("click", () => restoreHistory(state.historyIndex - 1)); $("redo-button").addEventListener("click", () => restoreHistory(state.historyIndex + 1)); $("zoom-out").addEventListener("click", () => applyZoom(state.zoom - .1)); $("zoom-in").addEventListener("click", () => applyZoom(state.zoom + .1)); $("fit-canvas").addEventListener("click", fitCanvas); $("fullscreen-button").addEventListener("click", () => document.documentElement.requestFullscreen?.()); $("export-button").addEventListener("click", exportWorkflow); $("retry-button").addEventListener("click", retryRun); $("cancel-button").addEventListener("click", cancelRun); $("send-agent").addEventListener("click", sendAgentMessage); $("view-logs").addEventListener("click", () => { $("run-event-bar").style.display = "flex"; $("run-log").hidden = !$("run-log").hidden; }); $("run-log-toggle").addEventListener("click", () => { $("run-log").hidden = !$("run-log").hidden; });
$("canvas").addEventListener("dragover", (event) => event.preventDefault()); $("canvas").addEventListener("drop", (event) => { event.preventDefault(); const kind = event.dataTransfer.getData("text/node-kind"); const bounds = $("canvas").getBoundingClientRect(); addNode(kind, (event.clientX - bounds.left + $("canvas").scrollLeft) / state.zoom, (event.clientY - bounds.top + $("canvas").scrollTop) / state.zoom); $("node-palette").hidden = true; });
$("canvas").addEventListener("pointerdown", (event) => { if (state.tool === "pan") { const x = event.clientX, y = event.clientY, left = $("canvas").scrollLeft, top = $("canvas").scrollTop; const move = (next) => { $("canvas").scrollLeft = left - next.clientX + x; $("canvas").scrollTop = top - next.clientY + y; }; const stop = () => { document.removeEventListener("pointermove", move); document.removeEventListener("pointerup", stop); }; document.addEventListener("pointermove", move); document.addEventListener("pointerup", stop); return; } state.selected = null; state.selectedEdge = null; state.artifactNodeID = ""; $("artifact-scope").textContent = "全部"; renderArtifacts(state.artifacts); render(); });
document.addEventListener("keydown", (event) => { if (event.key === "Delete" && state.tool === "select") deleteSelection(); });

(async function init() {
  $("project-id").textContent = `project: ${state.projectID}`;
  try { await loadRuntime(); await loadDefinitions(); await loadProject(); await restoreSession(); await restoreConversation(); await restoreRun(); if (!state.conversationID) addMessage("你好！我是你的创作助手。\n我可以帮你生成脚本、优化分镜、推荐素材。", "assistant"); if (!state.run) renderArtifacts([]); }
  catch (error) { addMessage(error.message, "error"); }
})();
