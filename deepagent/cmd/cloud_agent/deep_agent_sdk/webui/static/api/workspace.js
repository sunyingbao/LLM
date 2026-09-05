export function createWorkspaceAPI(request, basePath) {
  return {
    listFiles: ({ sessionID, path = ".", signal }) => request("list_files", {
      session_id: String(sessionID),
      path,
    }, { signal }),
    fileURL: (sessionID, path) => {
      const query = new URLSearchParams({ session_id: String(sessionID), path });
      return `${basePath}/file?${query}`;
    },
    listChanges: (sessionID, options = {}) => request("list_changes", {
      session_id: String(sessionID),
    }, options),
    getDiff: (sessionID, path, options = {}) => request("get_diff", {
      session_id: String(sessionID),
      path,
    }, options),
  };
}
