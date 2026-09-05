export function resumeReference(event, payload) {
  return {
    turn_id: event.turn_id || "",
    checkpoint_id: payload?.checkpoint_id || "",
    interrupt_id: payload?.interrupt_id || "",
  };
}

export function planInputContent(answers, note = "") {
  return [...(answers || []), note]
    .map((value) => String(value || "").trim())
    .filter(Boolean)
    .join("\n\n");
}
