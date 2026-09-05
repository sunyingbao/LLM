export function createFilesFeature({ api, store, fetchFn = globalThis.fetch?.bind(globalThis) }) {
  let previewController = null;

  async function openTask(view) {
    clear();
    const taskID = String(view?.session?.session_id || "");
    if (taskID) await loadDirectory(taskID, ".");
  }

  async function loadDirectory(taskID, path = ".") {
    const id = String(taskID);
    const current = store.getState().inspector.fileTree.get(path);
    if (current?.loaded || current?.loading) return current.files || [];
    store.dispatch({ type: "inspector/directoryLoading", taskID: id, path });
    const payload = await api.listFiles({ sessionID: id, path });
    if (store.getState().catalog.selectedTaskID !== id) return [];
    const files = payload.files || [];
    store.dispatch({ type: "inspector/directoryLoaded", taskID: id, path, files });
    return files;
  }

  async function toggleDirectory(path) {
    const state = store.getState();
    const branch = state.inspector.fileTree.get(path);
    if (!branch?.loaded) await loadDirectory(state.catalog.selectedTaskID, path);
    store.dispatch({ type: "inspector/directoryToggled", path });
  }

  async function selectFile(file) {
    if (!file) return;
    if (file.is_dir) {
      await toggleDirectory(file.path);
      return;
    }
    previewController?.abort();
    previewController = new AbortController();
    const taskID = store.getState().catalog.selectedTaskID;
    const preview = { ...file, url: api.fileURL(taskID, file.path), loading: isText(file.media_type) };
    store.dispatch({ type: "inspector/fileSelected", file: preview });
    if (!preview.loading || !fetchFn) return;
    try {
      const response = await fetchFn(preview.url, { signal: previewController.signal });
      if (!response.ok) throw new Error(response.statusText || "Failed to open file");
      const content = await response.text();
      store.dispatch({ type: "inspector/fileContentLoaded", taskID, path: file.path, content });
    } catch (error) {
      if (error.name !== "AbortError") store.dispatch({ type: "ui/error", error: error.message });
    }
  }

  async function refreshFiles() {
    const taskID = store.getState().catalog.selectedTaskID;
    if (!taskID) return [];
    store.dispatch({ type: "inspector/filesCleared" });
    return loadDirectory(taskID, ".");
  }

  function clear() {
    previewController?.abort();
    previewController = null;
    store.dispatch({ type: "inspector/filesCleared" });
  }

  return { openTask, loadDirectory, toggleDirectory, selectFile, refreshFiles, clear };
}

function isText(mediaType) {
  const value = String(mediaType || "").toLocaleLowerCase();
  return value.startsWith("text/") || value.includes("json") || value.includes("yaml") || value.includes("javascript");
}
