#!/usr/bin/env python3
"""Cloud Agent environment-portable E2E runner."""

from __future__ import annotations

import argparse
import dataclasses
import datetime as dt
import json
import os
import queue
import socket
import sys
import threading
import time
import traceback
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Callable


API_PREFIX = "/ad/aic_agent_sdk"
TERMINAL_EVENT_TYPES = {"TURN_FINISHED", "TURN_INTERRUPTED", "ERROR", "COMPACT_INTERRUPTED"}
SUCCESS_TERMINAL_EVENT_TYPES = {"TURN_FINISHED"}
ASSISTANT_EVENT_TYPES = {"ASSISTANT_MESSAGE", "ASSISTANT_DELTA"}
USER_INPUT_EVENT_TYPES = {"TURN_STARTED"}
LOG_ID_HEADERS = ("x-tt-logid", "x-tt-trace-id", "x-request-id", "logid")


class E2EError(Exception):
    pass


class SkipCase(Exception):
    pass


@dataclasses.dataclass
class Profile:
    name: str
    base_url: str
    headers: dict[str, str]
    timeouts: dict[str, int]
    features: dict[str, bool]
    project_prefix: str = "e2e"
    keep_artifacts: bool = True

    @classmethod
    def load(cls, path: Path) -> "Profile":
        raw = json.loads(path.read_text())
        return cls(
            name=str(raw.get("name") or path.stem),
            base_url=str(raw["base_url"]).rstrip("/"),
            headers={str(k): str(v) for k, v in dict(raw.get("headers") or {}).items()},
            timeouts={str(k): int(v) for k, v in dict(raw.get("timeouts") or {}).items()},
            features={str(k): bool(v) for k, v in dict(raw.get("features") or {}).items()},
            project_prefix=str(raw.get("project_prefix") or "e2e"),
            keep_artifacts=bool(raw.get("keep_artifacts", True)),
        )

    def timeout_seconds(self, name: str, default_ms: int) -> float:
        return float(self.timeouts.get(name, default_ms)) / 1000.0

    def feature_enabled(self, name: str, default: bool = False) -> bool:
        return bool(self.features.get(name, default))


@dataclasses.dataclass
class CaseContext:
    run_id: str
    case_name: str
    profile: Profile
    artifact_dir: Path
    client: "CloudAgentClient"
    session_id: str = ""
    thread_id: str = ""
    turn_id: str = ""
    message_id: str = ""
    logids: list[str] = dataclasses.field(default_factory=list)

    def case_dir(self) -> Path:
        path = self.artifact_dir / self.case_name
        path.mkdir(parents=True, exist_ok=True)
        return path

    def add_logids(self, values: list[str]) -> None:
        for value in values:
            if value and value not in self.logids:
                self.logids.append(value)


@dataclasses.dataclass
class CaseResult:
    name: str
    status: str
    started_at: str
    finished_at: str
    session_id: str = ""
    thread_id: str = ""
    turn_id: str = ""
    message_id: str = ""
    logids: list[str] = dataclasses.field(default_factory=list)
    failure: str = ""
    artifacts: dict[str, str] = dataclasses.field(default_factory=dict)


class ArtifactRecorder:
    def __init__(self, root: Path):
        self.root = root
        self.root.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()

    def append_request(self, case_name: str, item: dict[str, Any]) -> None:
        case_dir = self.root / case_name
        case_dir.mkdir(parents=True, exist_ok=True)
        with self._lock:
            with (case_dir / "requests.jsonl").open("a") as f:
                f.write(json.dumps(item, ensure_ascii=False, default=str) + "\n")

    def write_json(self, case_name: str, name: str, value: Any) -> Path:
        case_dir = self.root / case_name
        case_dir.mkdir(parents=True, exist_ok=True)
        path = case_dir / name
        path.write_text(json.dumps(value, ensure_ascii=False, indent=2, default=str))
        return path

    def write_text(self, case_name: str, name: str, value: str) -> Path:
        case_dir = self.root / case_name
        case_dir.mkdir(parents=True, exist_ok=True)
        path = case_dir / name
        path.write_text(value)
        return path


class CloudAgentClient:
    def __init__(self, profile: Profile, artifacts: ArtifactRecorder):
        self.profile = profile
        self.artifacts = artifacts
        self._opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.last_logids: list[str] = []

    def _headers(self, extra: dict[str, str] | None = None) -> dict[str, str]:
        headers = dict(self.profile.headers)
        if extra:
            headers.update(extra)
        return headers

    def _url(self, path: str) -> str:
        if not path.startswith("/"):
            path = "/" + path
        return self.profile.base_url + path

    def post_json(self, case_name: str, name: str, body: dict[str, Any]) -> dict[str, Any]:
        url = self._url(f"{API_PREFIX}/{name}")
        data = json.dumps(body).encode("utf-8")
        request = urllib.request.Request(
            url,
            data=data,
            headers=self._headers({"Content-Type": "application/json"}),
            method="POST",
        )
        started = time.time()
        record: dict[str, Any] = {"method": "POST", "name": name, "url": url, "body": body}
        try:
            with self._opener.open(request, timeout=self.profile.timeout_seconds("request_ms", 10000)) as response:
                raw = response.read()
                payload = json.loads(raw.decode("utf-8") or "{}")
                logids = response_logids(response.headers)
                self.last_logids = logids
                record.update(
                    {
                        "status": response.status,
                        "elapsed_ms": int((time.time() - started) * 1000),
                        "logids": logids,
                        "response": truncate_text(raw.decode("utf-8", "replace"), 12000),
                    }
                )
        except Exception as exc:
            record.update({"elapsed_ms": int((time.time() - started) * 1000), "error": repr(exc)})
            self.artifacts.append_request(case_name, record)
            raise E2EError(f"{name} request failed: {exc}") from exc
        self.artifacts.append_request(case_name, record)
        check_base_resp(name, payload)
        return payload

    def get_text(self, case_name: str, path: str, query: dict[str, str]) -> tuple[str, list[str]]:
        url = self._url(path) + "?" + urllib.parse.urlencode(query)
        request = urllib.request.Request(url, headers=self._headers(), method="GET")
        started = time.time()
        record: dict[str, Any] = {"method": "GET", "url": url}
        try:
            with self._opener.open(request, timeout=self.profile.timeout_seconds("request_ms", 10000)) as response:
                raw = response.read()
                text = raw.decode("utf-8", "replace")
                logids = response_logids(response.headers)
                self.last_logids = logids
                record.update(
                    {
                        "status": response.status,
                        "elapsed_ms": int((time.time() - started) * 1000),
                        "logids": logids,
                        "response": truncate_text(text, 12000),
                    }
                )
        except Exception as exc:
            record.update({"elapsed_ms": int((time.time() - started) * 1000), "error": repr(exc)})
            self.artifacts.append_request(case_name, record)
            raise E2EError(f"GET {path} failed: {exc}") from exc
        self.artifacts.append_request(case_name, record)
        return text, logids

    def create_session(self, ctx: CaseContext, title: str, project_name: str) -> dict[str, Any]:
        payload = self.post_json(ctx.case_name, "create_session", {"title": title, "project_name": project_name})
        ctx.add_logids(self.last_logids)
        session = get_session_from_payload(payload)
        session_id = str_value(session, "session_id")
        if not session_id:
            raise E2EError("create_session response missing session_view.session.session_id")
        ctx.session_id = session_id
        return payload

    def submit_input(self, ctx: CaseContext, content: str) -> dict[str, Any]:
        payload = self.post_json(ctx.case_name, "submit_input", {"session_id": ctx.session_id, "content": content})
        ctx.add_logids(self.last_logids)
        message = first_obj(payload, "message", "Message")
        thread_id = str_value(message, "thread_id")
        message_id = str_value(message, "message_id")
        if thread_id:
            ctx.thread_id = thread_id
        if message_id:
            ctx.message_id = message_id
        if not ctx.thread_id:
            raise E2EError("submit_input response missing message.thread_id")
        return payload

    def get_session(self, ctx: CaseContext) -> dict[str, Any]:
        payload = self.post_json(ctx.case_name, "get_session", {"session_id": ctx.session_id, "include_threads": True})
        ctx.add_logids(self.last_logids)
        return payload

    def list_sessions(self, ctx: CaseContext, project_name: str) -> dict[str, Any]:
        payload = self.post_json(ctx.case_name, "list_sessions", {"project_name": project_name, "limit": 100})
        ctx.add_logids(self.last_logids)
        return payload

    def list_timeline(self, ctx: CaseContext, limit: int = 200) -> list[dict[str, Any]]:
        payload = self.post_json(ctx.case_name, "list_timeline", {"session_id": ctx.session_id, "limit": limit})
        ctx.add_logids(self.last_logids)
        events = [normalize_event(e) for e in list_value(payload, "events")]
        self.artifacts.write_json(ctx.case_name, "timeline_latest.json", events)
        return events

    def list_files(self, ctx: CaseContext, path: str = ".") -> list[dict[str, Any]]:
        payload = self.post_json(ctx.case_name, "list_files", {"session_id": ctx.session_id, "path": path})
        ctx.add_logids(self.last_logids)
        return [dict(item) for item in list_value(payload, "files")]

    def read_file(self, ctx: CaseContext, path: str) -> str:
        text, logids = self.get_text(ctx.case_name, f"{API_PREFIX}/file", {"session_id": ctx.session_id, "path": path})
        ctx.add_logids(logids)
        self.artifacts.write_text(ctx.case_name, "file_read_response.txt", text)
        return text

    def open_timeline_stream(self, ctx: CaseContext, recover_queue_id: str = "") -> urllib.response.addinfourl:
        url = self._url(f"{API_PREFIX}/subscribe_timeline")
        body: dict[str, Any] = {"session_id": ctx.session_id}
        if recover_queue_id:
            body["recover_queue_id"] = recover_queue_id
        data = json.dumps(body).encode("utf-8")
        request = urllib.request.Request(
            url,
            data=data,
            headers=self._headers({"Content-Type": "application/json"}),
            method="POST",
        )
        return self._opener.open(request, timeout=self.profile.timeout_seconds("stream_ms", 180000))


class TimelineStreamCollector:
    def __init__(self, ctx: CaseContext):
        self.ctx = ctx
        self.frames: list[dict[str, Any]] = []
        self.errors: list[str] = []
        self.queue_ids: list[str] = []
        self.event_ids: list[str] = []
        self._stop = threading.Event()
        self._ready: queue.Queue[str] = queue.Queue(maxsize=1)
        self._response: Any = None
        self._thread: threading.Thread | None = None

    def start(self, recover_queue_id: str = "") -> None:
        self._thread = threading.Thread(target=self._run, args=(recover_queue_id,), daemon=True)
        self._thread.start()
        try:
            marker = self._ready.get(timeout=self.ctx.profile.timeout_seconds("request_ms", 10000))
        except queue.Empty as exc:
            raise E2EError("subscribe_timeline did not open before deadline") from exc
        if marker != "ready":
            raise E2EError(marker)

    def stop(self) -> None:
        self._stop.set()
        if self._response is not None:
            try:
                self._response.close()
            except Exception:
                pass
        if self._thread is not None:
            self._thread.join(timeout=2)

    def _run(self, recover_queue_id: str) -> None:
        try:
            with self.ctx.client.open_timeline_stream(self.ctx, recover_queue_id) as response:
                self._response = response
                self._ready.put("ready")
                self._read_sse(response)
        except Exception as exc:
            if not self._stop.is_set():
                self.errors.append(repr(exc))
                try:
                    self._ready.put(f"subscribe_timeline failed: {exc}")
                except queue.Full:
                    pass

    def _read_sse(self, response: Any) -> None:
        event_name = ""
        data_lines: list[str] = []
        while not self._stop.is_set():
            try:
                raw = response.readline()
            except socket.timeout:
                continue
            except Exception as exc:
                if not self._stop.is_set():
                    self.errors.append(repr(exc))
                return
            if not raw:
                return
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            if line == "":
                self._emit_frame(event_name, "\n".join(data_lines))
                event_name = ""
                data_lines = []
                continue
            if line.startswith("event:"):
                event_name = line[len("event:") :].strip()
                continue
            if line.startswith("data:"):
                data_lines.append(line[len("data:") :].strip())

    def _emit_frame(self, name: str, data: str) -> None:
        if not name and not data:
            return
        payload: Any = data
        try:
            payload = json.loads(data) if data else {}
        except json.JSONDecodeError:
            pass
        frame = {"event": name, "data": payload, "received_at": iso_now()}
        self.frames.append(frame)
        if isinstance(payload, dict):
            queue_id = str_value(payload, "queue_id")
            if queue_id:
                self.queue_ids.append(queue_id)
            event = first_obj(payload, "event", "Event")
            if isinstance(event, dict):
                event_id = str_value(event, "event_id")
                if event_id:
                    self.event_ids.append(event_id)


def scenario_basic_turn(ctx: CaseContext) -> None:
    project_name = f"{ctx.profile.project_prefix}_basic_{ctx.run_id}"
    ctx.client.create_session(ctx, "Cloud Agent E2E Basic", project_name)
    ctx.client.submit_input(
        ctx,
        "Reply with one short sentence. The sentence must include this exact run id: "
        f"{ctx.run_id}. Do not call tools.",
    )
    events = wait_for_terminal(ctx, require_success=True)
    assert_has_event_type(events, USER_INPUT_EVENT_TYPES, "current turn user input")
    assert_has_event_type(events, ASSISTANT_EVENT_TYPES, "assistant output")
    text = assistant_text(events)
    if ctx.run_id not in text:
        raise E2EError(f"assistant output did not include run_id {ctx.run_id!r}; text={truncate_text(text, 500)!r}")
    session_payload = ctx.client.get_session(ctx)
    session = get_session_from_payload(session_payload)
    if str_value(session, "session_id") != ctx.session_id:
        raise E2EError("get_session did not return the created session")
    if ctx.thread_id and str_value(session, "main_thread_id") not in ("", ctx.thread_id):
        raise E2EError("get_session main_thread_id does not match submitted thread")


def scenario_workspace_minimal(ctx: CaseContext) -> None:
    if not ctx.profile.feature_enabled("file_api", True):
        raise SkipCase("profile features.file_api=false")
    project_name = f"{ctx.profile.project_prefix}_workspace_{ctx.run_id}"
    file_name = f"e2e_probe_{ctx.run_id}.txt"
    ctx.client.create_session(ctx, "Cloud Agent E2E Workspace", project_name)
    ctx.client.submit_input(
        ctx,
        "In the current workspace, create a file named "
        f"{file_name}. The file content must be exactly this run id on one line: {ctx.run_id}. "
        "After writing the file, reply briefly.",
    )
    wait_for_terminal(ctx, require_success=True)
    wait_for_file(ctx, file_name)
    content = ctx.client.read_file(ctx, file_name)
    if ctx.run_id not in content:
        raise E2EError(f"{file_name} did not contain run_id {ctx.run_id!r}; content={content!r}")


def scenario_streaming_recovery(ctx: CaseContext) -> None:
    project_name = f"{ctx.profile.project_prefix}_stream_{ctx.run_id}"
    ctx.client.create_session(ctx, "Cloud Agent E2E Stream", project_name)
    collector = TimelineStreamCollector(ctx)
    collector.start()
    try:
        ctx.client.submit_input(
            ctx,
            "Write three short numbered lines. Each line must include this exact run id: "
            f"{ctx.run_id}. Do not call tools.",
        )
        events = wait_for_terminal(ctx, require_success=True)
    finally:
        collector.stop()
        ctx.client.artifacts.write_json(ctx.case_name, "sse_frames.json", collector.frames)
    if not collector.queue_ids:
        raise E2EError("SSE stream did not receive a queue frame")
    event_frames = [f for f in collector.frames if f.get("event") == "event"]
    if not event_frames:
        raise E2EError(f"SSE stream did not receive event frames; errors={collector.errors}")
    assert_timeline_envelopes(events)
    if collector.event_ids:
        history_ids = {str_value(event, "event_id") for event in events}
        if not any(event_id in history_ids for event_id in collector.event_ids):
            raise E2EError("no SSE event id was found in final list_timeline history")


SCENARIOS: dict[str, Callable[[CaseContext], None]] = {
    "basic_turn": scenario_basic_turn,
    "workspace_minimal": scenario_workspace_minimal,
    "streaming_recovery": scenario_streaming_recovery,
}

SUITES: dict[str, list[str]] = {
    "p0": ["basic_turn", "workspace_minimal", "streaming_recovery"],
}


def wait_for_terminal(ctx: CaseContext, require_success: bool) -> list[dict[str, Any]]:
    deadline = time.time() + ctx.profile.timeout_seconds("turn_ms", 180000)
    last_events: list[dict[str, Any]] = []
    while time.time() < deadline:
        events = ctx.client.list_timeline(ctx)
        last_events = current_thread_events(events, ctx.thread_id)
        terminal = latest_terminal_event(last_events)
        if terminal:
            ctx.turn_id = str_value(terminal, "turn_id")
            ctx.client.artifacts.write_json(ctx.case_name, "timeline_final.json", events)
            if require_success and str_value(terminal, "event_type") not in SUCCESS_TERMINAL_EVENT_TYPES:
                raise E2EError(f"turn terminal event was not success: {terminal}")
            return last_events
        time.sleep(2)
    ctx.client.artifacts.write_json(ctx.case_name, "timeline_final.json", last_events)
    raise E2EError(f"turn did not reach terminal event before deadline; recent={timeline_summary(last_events)}")


def wait_for_file(ctx: CaseContext, file_name: str) -> None:
    deadline = time.time() + ctx.profile.timeout_seconds("file_ms", 60000)
    last_files: list[dict[str, Any]] = []
    while time.time() < deadline:
        files = ctx.client.list_files(ctx, ".")
        last_files = files
        if any(str_value(item, "name") == file_name or str_value(item, "path").endswith("/" + file_name) or str_value(item, "path") == file_name for item in files):
            ctx.client.artifacts.write_json(ctx.case_name, "files_final.json", files)
            return
        time.sleep(2)
    ctx.client.artifacts.write_json(ctx.case_name, "files_final.json", last_files)
    raise E2EError(f"file {file_name} not found before deadline; files={last_files}")


def current_thread_events(events: list[dict[str, Any]], thread_id: str) -> list[dict[str, Any]]:
    if not thread_id:
        return events
    filtered = [event for event in events if str_value(event, "thread_id") in ("", thread_id)]
    return filtered or events


def latest_terminal_event(events: list[dict[str, Any]]) -> dict[str, Any] | None:
    for event in reversed(events):
        if str_value(event, "event_type") in TERMINAL_EVENT_TYPES:
            return event
    return None


def assert_has_event_type(events: list[dict[str, Any]], event_types: set[str], label: str) -> None:
    if not any(str_value(event, "event_type") in event_types for event in events):
        raise E2EError(f"timeline missing {label}; recent={timeline_summary(events)}")


def assert_timeline_envelopes(events: list[dict[str, Any]]) -> None:
    for event in events:
        if not str_value(event, "event_id"):
            raise E2EError(f"timeline event missing event_id: {event}")
        if not str_value(event, "event_type"):
            raise E2EError(f"timeline event missing event_type: {event}")
        if "payload" not in event:
            raise E2EError(f"timeline event missing payload: {event}")


def assistant_text(events: list[dict[str, Any]]) -> str:
    chunks: list[str] = []
    for event in events:
        event_type = str_value(event, "event_type")
        payload = first_obj(event, "payload")
        if event_type == "ASSISTANT_DELTA":
            chunks.append(str_value(payload, "delta"))
        if event_type == "ASSISTANT_MESSAGE":
            for part in list_value(payload, "parts"):
                if isinstance(part, dict):
                    chunks.append(str_value(part, "text"))
    return "".join(chunks)


def timeline_summary(events: list[dict[str, Any]], limit: int = 12) -> list[dict[str, Any]]:
    summary = []
    for event in events[-limit:]:
        summary.append(
            {
                "event_id": str_value(event, "event_id"),
                "event_type": str_value(event, "event_type"),
                "thread_id": str_value(event, "thread_id"),
                "turn_id": str_value(event, "turn_id"),
                "payload": truncate_text(json.dumps(first_obj(event, "payload"), ensure_ascii=False), 300),
            }
        )
    return summary


def normalize_event(event: Any) -> dict[str, Any]:
    if not isinstance(event, dict):
        return {}
    out = dict(event)
    payload = out.get("payload")
    if isinstance(payload, str):
        try:
            out["payload"] = json.loads(payload)
        except json.JSONDecodeError:
            out["payload"] = {"raw": payload}
    elif payload is None:
        out["payload"] = {}
    return out


def check_base_resp(name: str, payload: dict[str, Any]) -> None:
    base = first_obj(payload, "BaseResp", "base_resp")
    status = int_value(base, "StatusCode", "status_code")
    if status != 0:
        message = str_value(base, "StatusMessage") or str_value(base, "status_message") or f"{name} failed"
        raise E2EError(f"{name} returned BaseResp status={status}: {message}")


def get_session_from_payload(payload: dict[str, Any]) -> dict[str, Any]:
    view = first_obj(payload, "session_view", "SessionView")
    return first_obj(view, "session", "Session")


def first_obj(value: Any, *keys: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        return {}
    for key in keys:
        for variant in key_variants(key):
            item = value.get(variant)
            if isinstance(item, dict):
                return item
    return {}


def list_value(value: Any, key: str) -> list[Any]:
    if not isinstance(value, dict):
        return []
    for variant in key_variants(key):
        item = value.get(variant)
        if isinstance(item, list):
            return item
    return []


def str_value(value: Any, *keys: str) -> str:
    if not isinstance(value, dict):
        return ""
    for key in keys:
        for variant in key_variants(key):
            if variant in value and value[variant] is not None:
                return str(value[variant])
    return ""


def key_variants(value: str) -> list[str]:
    camel = to_camel(value)
    pascal = camel[:1].upper() + camel[1:]
    return list(dict.fromkeys([value, camel, pascal]))


def int_value(value: Any, *keys: str) -> int:
    raw = str_value(value, *keys)
    if not raw:
        return 0
    try:
        return int(raw)
    except ValueError:
        return 0


def to_camel(value: str) -> str:
    parts = value.split("_")
    return parts[0] + "".join(part[:1].upper() + part[1:] for part in parts[1:])


def response_logids(headers: Any) -> list[str]:
    found: list[str] = []
    for name in LOG_ID_HEADERS:
        value = headers.get(name) if hasattr(headers, "get") else ""
        if value:
            found.append(str(value))
    return found


def truncate_text(value: str, limit: int) -> str:
    if len(value) <= limit:
        return value
    return value[:limit] + "...<truncated>"


def iso_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat()


def new_run_id() -> str:
    stamp = dt.datetime.now().strftime("%Y%m%d_%H%M%S")
    return f"{stamp}_{os.getpid()}"


def run_case(name: str, scenario: Callable[[CaseContext], None], base_ctx: CaseContext) -> CaseResult:
    started = iso_now()
    ctx = dataclasses.replace(base_ctx, case_name=name, session_id="", thread_id="", turn_id="", message_id="", logids=[])
    status = "passed"
    failure = ""
    try:
        scenario(ctx)
    except SkipCase as exc:
        status = "skipped"
        failure = str(exc)
    except Exception as exc:
        status = "failed"
        failure = f"{exc}"
        ctx.client.artifacts.write_text(name, "failure.txt", failure + "\n\n" + traceback.format_exc())
    artifacts = {}
    case_dir = ctx.case_dir()
    for path in sorted(case_dir.glob("*")):
        if path.is_file():
            artifacts[path.stem] = str(path)
    result = CaseResult(
        name=name,
        status=status,
        started_at=started,
        finished_at=iso_now(),
        session_id=ctx.session_id,
        thread_id=ctx.thread_id,
        turn_id=ctx.turn_id,
        message_id=ctx.message_id,
        logids=ctx.logids,
        failure=failure,
        artifacts=artifacts,
    )
    print_case_result(result, base_ctx.artifact_dir)
    return result


def print_case_result(result: CaseResult, artifact_dir: Path) -> None:
    prefix = {"passed": "PASS", "failed": "FAIL", "skipped": "SKIP"}.get(result.status, result.status.upper())
    print(f"{prefix} {result.name}", flush=True)
    if result.status != "passed":
        print(f"  failure={result.failure}", flush=True)
    print(f"  session_id={result.session_id}", flush=True)
    print(f"  thread_id={result.thread_id}", flush=True)
    print(f"  turn_id={result.turn_id}", flush=True)
    print(f"  message_id={result.message_id}", flush=True)
    if result.logids:
        print(f"  logids={','.join(result.logids)}", flush=True)
    print(f"  artifact_dir={artifact_dir / result.name}", flush=True)


def select_cases(suite: str, case_names: list[str]) -> list[str]:
    if case_names:
        unknown = [name for name in case_names if name not in SCENARIOS]
        if unknown:
            raise E2EError(f"unknown case(s): {', '.join(unknown)}")
        return case_names
    if suite not in SUITES:
        raise E2EError(f"unknown suite {suite!r}; available: {', '.join(sorted(SUITES))}")
    return SUITES[suite]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run Cloud Agent environment-portable E2E scenarios")
    parser.add_argument("--profile", required=True, help="Path to environment profile JSON")
    parser.add_argument("--suite", default="p0", help="Suite name to run; default: p0")
    parser.add_argument("--case", action="append", default=[], help="Run one case by name; may be repeated")
    parser.add_argument("--out", default="", help="Report JSON path; default: <artifact-dir>/<run-id>/report.json")
    parser.add_argument("--artifact-dir", default="/tmp/cloud_agent_e2e", help="Directory for run artifacts")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    profile = Profile.load(Path(args.profile))
    run_id = new_run_id()
    artifact_root = Path(args.artifact_dir) / run_id
    artifacts = ArtifactRecorder(artifact_root)
    client = CloudAgentClient(profile, artifacts)
    base_ctx = CaseContext(run_id=run_id, case_name="", profile=profile, artifact_dir=artifact_root, client=client)
    case_names = select_cases(args.suite, args.case)
    started = iso_now()
    print(f"Cloud Agent E2E run_id={run_id} profile={profile.name} suite={args.suite}", flush=True)
    print(f"artifact_dir={artifact_root}", flush=True)
    results = [run_case(name, SCENARIOS[name], base_ctx) for name in case_names]
    status = "passed"
    if any(result.status == "failed" for result in results):
        status = "failed"
    elif any(result.status == "skipped" for result in results):
        status = "skipped"
    report = {
        "run_id": run_id,
        "profile": profile.name,
        "suite": args.suite,
        "started_at": started,
        "finished_at": iso_now(),
        "status": status,
        "artifact_dir": str(artifact_root),
        "cases": [dataclasses.asdict(result) for result in results],
    }
    report_path = Path(args.out) if args.out else artifact_root / "report.json"
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(f"report={report_path}", flush=True)
    print(f"status={status}", flush=True)
    return 1 if status == "failed" else 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except E2EError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(2)
