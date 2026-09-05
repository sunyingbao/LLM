export function createComposer(root, actions) {
  if (!root) throw new Error("composer root is missing");
  const input = root.querySelector("[data-composer]");
  const send = root.querySelector("[data-send]");
  const plan = root.querySelector("[data-plan-mode]");
  const compact = root.querySelector("[data-compact]");

  input.addEventListener("input", () => {
    actions.setDraft?.(input.value);
    resize(input);
  });
  input.addEventListener("keydown", (event) => {
    if (event.isComposing || event.keyCode === 229) return;
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submit(input, actions);
    }
  });
  send.addEventListener("click", () => submit(input, actions));
  plan.addEventListener("click", () => actions.togglePlanMode?.());
  compact.addEventListener("click", () => run(actions.compactContext?.()));

  return {
    render(state) {
      const running = state.task.runState === "running" || state.task.runState === "stopping";
      if (input.value !== state.task.draft) input.value = state.task.draft;
      plan.dataset.active = String(state.task.planMode);
      plan.setAttribute("aria-pressed", String(state.task.planMode));
      send.textContent = running ? "■" : "↑";
      send.setAttribute("aria-label", running ? "Stop task" : "Send message");
      input.disabled = state.task.runState === "stopping";
      compact.disabled = !state.catalog.selectedTaskID;
      const status = root.querySelector("[data-composer-status]");
      status.textContent = state.task.submitError || state.ui.error || "";
      status.classList.toggle("error", Boolean(status.textContent));
      renderPendingSummary(root.querySelector("[data-pending-slot]"), state.task.pending);
      resize(input);
    },
  };
}

function submit(input, actions) {
  const state = input.closest("[data-workspace]")?.querySelector("[data-run-status]")?.dataset.state;
  if (state === "running" || state === "stopping") {
    run(actions.stopTask?.());
    return;
  }
  run(actions.submitMessage?.(input.value));
}

function renderPendingSummary(root, pending) {
  root.replaceChildren();
  if (!pending) return;
  const label = document.createElement("span");
  label.className = "pending-summary";
  label.textContent = pending.kind === "approval" ? "Approval required below" : "Your input is required below";
  root.append(label);
}

function resize(input) {
  input.style.height = "auto";
  input.style.height = `${Math.min(input.scrollHeight, 180)}px`;
}

function run(promise) {
  promise?.catch(() => {});
}
