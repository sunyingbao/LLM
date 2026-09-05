import { commandTool } from "./tools.js";

export function terminalEntries(activities) {
  return (activities || [])
    .filter((activity) => activity.kind === "tool" && commandTool(activity))
    .map((activity) => ({
      id: activity.id,
      command: commandText(activity.arguments),
      output: outputText(activity),
      status: terminalStatus(activity),
      exitCode: exitCode(activity),
    }));
}

function commandText(args) {
  const command = args?.command ?? args?.cmd ?? "";
  return Array.isArray(command) ? command.join(" ") : String(command || "");
}

function outputText(activity) {
  if (activity.output) return activity.output;
  const result = activity.result || {};
  return String(result.output || [result.stdout, result.stderr].filter(Boolean).join("\n") || activity.error || "");
}

function exitCode(activity) {
  const value = activity.exitCode ?? activity.result?.exit_code ?? activity.result?.exitCode;
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
}

function terminalStatus(activity) {
  const code = exitCode(activity);
  if (activity.error || Number(activity.status) === 3 || (code !== null && code !== 0)) return "failed";
  if (activity.status === "completed" || Number(activity.status) === 2) return "completed";
  return "running";
}
