import { terminalEntries } from "../features/terminal.js";
import { parseUnifiedDiff } from "../features/changes.js";

const tabs = ["changes", "files", "terminal"];

export function createInspector(root, actions) {
  if (!root) throw new Error("inspector root is missing");
  root.innerHTML = `
    <header class="inspector-header">
      <div class="inspector-tabs" role="tablist" aria-label="Task inspector">
        ${tabs.map((tab) => `<button type="button" role="tab" data-inspector-tab="${tab}">${label(tab)}</button>`).join("")}
      </div>
      <button class="icon-button" type="button" data-collapse-inspector aria-label="Collapse inspector">›</button>
    </header>
    <section class="inspector-content" data-inspector-content></section>
  `;
  let state = null;
  root.addEventListener("click", (event) => {
    const tab = event.target.closest("[data-inspector-tab]");
    if (tab) actions.selectInspectorTab?.(tab.dataset.inspectorTab);
    if (event.target.closest("[data-collapse-inspector]")) actions.toggleInspector?.();
    if (event.target.closest("[data-refresh-files]")) run(actions.refreshFiles?.());
    if (event.target.closest("[data-refresh-changes]")) run(actions.refreshChanges?.());
    const file = event.target.closest("[data-file-path]");
    if (file) run(actions.selectFile?.(findFile(state, file.dataset.filePath)));
    const change = event.target.closest("[data-change-path]");
    if (change) run(actions.selectChange?.(findChange(state, change.dataset.changePath)));
    const line = event.target.closest("[data-diff-comment-line]");
    if (line) actions.startComment?.(state.inspector.selectedChangePath, line.dataset.diffCommentLine);
    if (event.target.closest("[data-cancel-diff-comment]")) actions.cancelComment?.();
  });
  root.addEventListener("submit", (event) => {
    const commentForm = event.target.closest("[data-diff-comment-form]");
    if (commentForm) {
      event.preventDefault();
      const target = state.inspector.commentTarget;
      actions.addAnnotation?.({
        path: target?.path,
        startLine: target?.line,
        endLine: target?.line,
        comment: commentForm.elements.comment.value,
      });
      return;
    }
    const reviewForm = event.target.closest("[data-review-form]");
    if (reviewForm) {
      event.preventDefault();
      run(actions.submitReview?.(reviewForm.elements.note.value));
    }
  });
  root.addEventListener("keydown", (event) => {
    const tab = event.target.closest?.("[data-inspector-tab]");
    if (!tab) return;
    const index = tabs.indexOf(tab.dataset.inspectorTab);
    let next = index;
    if (event.key === "ArrowRight") next = (index + 1) % tabs.length;
    else if (event.key === "ArrowLeft") next = (index + tabs.length - 1) % tabs.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = tabs.length - 1;
    else return;
    event.preventDefault();
    const target = root.querySelector(`[data-inspector-tab="${tabs[next]}"]`);
    target?.focus();
    actions.selectInspectorTab?.(tabs[next]);
  });

  return {
    render(nextState) {
      state = nextState;
      root.dataset.collapsed = String(Boolean(nextState.inspector.collapsed));
      const collapse = root.querySelector("[data-collapse-inspector]");
      collapse.textContent = nextState.inspector.collapsed ? "‹" : "›";
      collapse.setAttribute("aria-label", nextState.inspector.collapsed ? "Open task inspector" : "Collapse inspector");
      for (const tab of root.querySelectorAll("[data-inspector-tab]")) {
        const selected = tab.dataset.inspectorTab === nextState.inspector.tab;
        tab.setAttribute("aria-selected", String(selected));
        tab.tabIndex = selected ? 0 : -1;
      }
      const content = root.querySelector("[data-inspector-content]");
      content.replaceChildren(panel(nextState));
    },
  };
}

function panel(state) {
  if (state.inspector.tab === "files") return filesPanel(state);
  if (state.inspector.tab === "terminal") return terminalPanel(state);
  return changesPanel(state);
}

function changesPanel(state) {
  const panel = element("div", "changes-view");
  const toolbar = element("div", "inspector-toolbar");
  toolbar.append(element("strong", "", state.catalog.selectedProject || "Changes"));
  const refresh = button("↻", "Refresh changes");
  refresh.dataset.refreshChanges = "true";
  toolbar.append(refresh);
  panel.append(toolbar);

  if (!state.catalog.selectedTaskID) {
    panel.append(emptyInspector("changes", "Select a task to inspect file changes"));
    return panel;
  }

  const list = element("div", "change-list");
  if (state.inspector.changesLoading) {
    list.append(emptyInspector("changes", "Loading changes…"));
  } else if (!state.inspector.changes.length) {
    list.append(emptyInspector("changes"));
  } else {
    list.append(...state.inspector.changes.map((change) => changeRow(change, state)));
  }
  panel.append(list);
  panel.append(diffPanel(state));
  return panel;
}

function diffPanel(state) {
  const section = element("section", "change-diff");
  if (!state.inspector.selectedChangePath) {
    section.append(emptyInspector("diff", "Select a file to view patch"));
    return section;
  }
  const title = element("header", "change-diff-header", state.inspector.selectedChangePath);
  section.append(title);
  const diff = state.inspector.diff;
  if (state.inspector.diffLoading) {
    section.append(emptyInspector("diff", "Loading diff…"));
    return section;
  }
  const patch = typeof diff?.patch === "string" ? diff.patch : "";
  if (!patch) {
    section.append(emptyInspector("diff", "No diff available for this path"));
    return section;
  }
  section.append(diffContent(state, patch));
  if (diff?.truncated) {
    section.append(element("div", "change-diff-meta", "Diff truncated"));
  }
  return section;
}

function diffContent(state, patch) {
  const content = element("div", "change-diff-content");
  const diff = parseUnifiedDiff(patch);
  for (const hunk of diff.hunks) {
    content.append(element("div", "diff-hunk-header", hunk.header));
    for (const line of hunk.lines) content.append(diffLine(state, line));
  }
  if (!diff.hunks.length) content.append(element("pre", "diff-raw", patch));
  if (state.inspector.annotations.length) content.append(reviewForm(state.inspector.annotations));
  return content;
}

function diffLine(state, line) {
  const wrap = element("div", `diff-line-wrap ${line.kind}`);
  const row = element("div", "diff-line");
  row.append(element("span", "diff-line-number", line.oldLine ?? ""));
  row.append(element("span", "diff-line-number", line.newLine ?? ""));
  const code = element("code", "diff-line-code", line.text || " ");
  row.append(code);
  const commentLine = line.newLine;
  if (commentLine) {
    const comment = button("＋", `Comment on line ${commentLine}`);
    comment.className = "diff-comment-button";
    comment.dataset.diffCommentLine = String(commentLine);
    row.append(comment);
  }
  wrap.append(row);
  for (const annotation of annotationsAt(state, commentLine)) {
    wrap.append(element("div", "diff-annotation", annotation.comment));
  }
  const target = state.inspector.commentTarget;
  if (target?.path === state.inspector.selectedChangePath && target.line === commentLine) {
    wrap.append(diffCommentForm(commentLine));
  }
  return wrap;
}

function diffCommentForm(line) {
  const form = element("form", "diff-comment-form");
  form.dataset.diffCommentForm = "true";
  const textarea = element("textarea", "pending-textarea");
  textarea.name = "comment";
  textarea.rows = 2;
  textarea.placeholder = `Comment on line ${line}`;
  textarea.required = true;
  const actions = element("div", "pending-actions");
  const cancel = button("Cancel", "Cancel comment");
  cancel.className = "secondary-button";
  cancel.dataset.cancelDiffComment = "true";
  const save = button("Add comment", "Add comment");
  save.type = "submit";
  save.className = "primary-button";
  save.dataset.saveDiffComment = "true";
  actions.append(cancel, save);
  form.append(textarea, actions);
  return form;
}

function reviewForm(annotations) {
  const form = element("form", "diff-review-form");
  form.dataset.reviewForm = "true";
  form.append(element("strong", "", `${annotations.length} review comment${annotations.length === 1 ? "" : "s"}`));
  const note = element("textarea", "pending-textarea");
  note.name = "note";
  note.rows = 2;
  note.placeholder = "Optional overall note";
  const submit = button("Send review", "Send review to DeepAgent");
  submit.type = "submit";
  submit.className = "primary-button";
  form.append(note, submit);
  return form;
}

function annotationsAt(state, line) {
  if (!line) return [];
  return state.inspector.annotations.filter((annotation) => (
    annotation.path === state.inspector.selectedChangePath
    && line >= annotation.startLine
    && line <= annotation.endLine
  ));
}

function filesPanel(state) {
  const panel = element("div", "files-view");
  const toolbar = element("div", "inspector-toolbar");
  toolbar.append(element("strong", "", state.catalog.selectedProject || "Files"));
  const refresh = button("↻", "Refresh files");
  refresh.dataset.refreshFiles = "true";
  toolbar.append(refresh);
  panel.append(toolbar);
  if (!state.catalog.selectedTaskID) {
    panel.append(emptyInspector("files", "Select a task to inspect files"));
    return panel;
  }
  const preview = filePreview(state.inspector.filePreview);
  if (preview) panel.append(preview);
  const tree = element("div", "file-tree");
  const branch = state.inspector.fileTree.get(".");
  if (!branch || branch.loading) tree.append(emptyInspector("files", "Loading files…"));
  else if (!branch.files.length) tree.append(emptyInspector("files"));
  else tree.append(...branch.files.map((file) => fileNode(file, state, 0)));
  panel.append(tree);
  return panel;
}

function terminalPanel(state) {
  const panel = element("div", "terminal-view");
  const entries = terminalEntries(state.task.activities);
  if (!entries.length) {
    panel.append(emptyInspector("terminal"));
    return panel;
  }
  for (const entry of entries) {
    const article = element("article", `terminal-entry ${entry.status}`);
    const header = element("header", "terminal-entry-header");
    header.append(element("code", "", `$ ${entry.command || "command"}`), element("span", "", terminalLabel(entry)));
    article.append(header);
    if (entry.output) article.append(element("pre", "terminal-output", entry.output));
    panel.append(article);
  }
  return panel;
}

function fileNode(file, state, depth) {
  const wrap = element("div", "file-node-wrap");
  const row = button("", `Open ${file.name || file.path}`);
  row.className = `file-node${state.inspector.selectedPath === file.path ? " selected" : ""}`;
  row.dataset.filePath = file.path;
  row.style.paddingLeft = `${8 + depth * 14}px`;
  row.append(element("span", "file-icon", file.is_dir ? directoryIcon(state, file.path) : fileIcon(file.media_type)));
  row.append(element("span", "file-name", file.name || file.path));
  if (!file.is_dir && file.size) row.append(element("span", "file-size", formatBytes(file.size)));
  wrap.append(row);
  if (file.is_dir) {
    const branch = state.inspector.fileTree.get(file.path);
    if (branch?.expanded) {
      const children = element("div", "file-children");
      if (branch.loading) children.append(emptyInspector("files", "Loading…"));
      else children.append(...(branch.files || []).map((child) => fileNode(child, state, depth + 1)));
      wrap.append(children);
    }
  }
  return wrap;
}

function changeRow(change, state) {
  const row = button("", `${change.path}`);
  row.className = `change-node${state.inspector.selectedChangePath === change.path ? " selected" : ""}`;
  row.dataset.changePath = change.path;
  const badge = element("span", `change-status ${change.status || ""}`, String(change.status || ""));
  const count = fileCounts(change);
  row.append(badge);
  row.append(element("span", "file-name", change.path));
  if (count) row.append(element("span", "change-counts", count));
  return row;
}

function filePreview(file) {
  if (!file) return null;
  const preview = element("section", "file-preview");
  const header = element("header", "file-preview-header", file.name || file.path);
  preview.append(header);
  const media = String(file.media_type || "");
  if (file.content !== undefined) preview.append(element("pre", "file-text", file.content));
  else if (media.startsWith("image/")) preview.append(mediaElement("img", file));
  else if (media.startsWith("video/")) preview.append(mediaElement("video", file));
  else if (media.startsWith("audio/")) preview.append(mediaElement("audio", file));
  else {
    const link = element("a", "file-open-link", file.loading ? "Loading…" : "Open file");
    link.href = file.url;
    link.target = "_blank";
    link.rel = "noreferrer";
    preview.append(link);
  }
  return preview;
}

function mediaElement(tag, file) {
  const media = document.createElement(tag);
  media.src = file.url;
  if (tag === "img") media.alt = file.name || file.path;
  else media.controls = true;
  return media;
}

function findFile(state, path) {
  for (const branch of state?.inspector.fileTree.values() || []) {
    const file = (branch.files || []).find((item) => item.path === path);
    if (file) return file;
  }
  return null;
}

function emptyInspector(tab, message = "") {
  const element = document.createElement("div");
  element.className = "inspector-empty";
  element.textContent = message || `No ${label(tab).toLocaleLowerCase()} yet`;
  return element;
}

function findChange(state, path) {
  if (!path) return null;
  const target = String(path);
  return (state?.inspector.changes || []).find((item) => item.path === target) || null;
}

function fileCounts(change) {
  const added = Number.isFinite(Number(change?.additions)) ? Number(change.additions) : 0;
  const deleted = Number.isFinite(Number(change?.deletions)) ? Number(change.deletions) : 0;
  if (!added && !deleted) return "";
  if (!deleted) return `+${added}`;
  if (!added) return `-${deleted}`;
  return `+${added} / -${deleted}`;
}

function label(value) {
  return `${value.slice(0, 1).toUpperCase()}${value.slice(1)}`;
}

function button(text, label) {
  const value = element("button", "icon-button", text);
  value.type = "button";
  value.setAttribute("aria-label", label);
  return value;
}

function element(tag, className, text) {
  const value = document.createElement(tag);
  if (className) value.className = className;
  if (text !== undefined) value.textContent = text;
  return value;
}

function directoryIcon(state, path) {
  return state.inspector.fileTree.get(path)?.expanded ? "⌄" : "›";
}

function fileIcon(mediaType) {
  const value = String(mediaType || "");
  if (value.startsWith("image/")) return "▧";
  if (value.startsWith("video/")) return "▶";
  if (value.startsWith("audio/")) return "♪";
  return "·";
}

function formatBytes(value) {
  const size = Number(value || 0);
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${Math.round(size / 1024)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

function terminalLabel(entry) {
  if (entry.status === "running") return "running";
  if (entry.exitCode !== null) return `exit ${entry.exitCode}`;
  return entry.status;
}

function run(promise) {
  promise?.catch(() => {});
}
