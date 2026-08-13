#!/usr/bin/env python3
"""Local development manager for cmd/cloud_agent services."""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import signal
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent
REPO_ROOT = ROOT.parents[1]
RUNTIME_DIR = ROOT / "runtime" / "dev"
PID_DIR = RUNTIME_DIR / "pids"
LOG_DIR = RUNTIME_DIR / "logs"
GENERATED_DIR = RUNTIME_DIR / "generated"
BIN_DIR = RUNTIME_DIR / "bin"

CONFIG_VERSION = 1
DEFAULT_CONFIG_PATH = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config")) / "aic_agent_sdk" / "dev_config.json"
DEFAULT_MYSQL_DSN = "ac_test:ac_test_pwd_20260416@tcp(127.0.0.1:3306)/agent_coordinator_test?charset=utf8mb4&parseTime=True&loc=Local"
DEFAULT_WORKSPACE_ROOT = str(Path.home() / "deepagent_workspace")
DEFAULT_ENV_FILE = str(Path.home() / ".config" / "aic_agent_sdk" / "model.env")
DEFAULT_WORKER_CONFIG = str(ROOT / "aic_agent_sdk_worker" / "conf" / "worker.local.yml")

# The local Worker profile fixes the provider and model. The env file only
# supplies referenced secrets, so inherited provider variables cannot change
# runtime behavior.
MODEL_PROVIDER_ENV_KEYS = {
    "OPENROUTER_API_KEY",
    "OPENROUTER_MODEL",
    "KIMI_API_KEY",
    "KIMI_BASE_URL",
    "KIMI_MODEL",
    "KIMI_LOG_ID",
    "OPENAI_API_KEY",
    "OPENAI_BASE_URL",
    "OPENAI_MODEL",
    "ARK_API_KEY",
    "ARK_MODEL",
}

@dataclass(frozen=True)
class Service:
    name: str
    cwd: Path
    port: int | None


SERVICES = {
    "aic_agent_sdk_session": Service("aic_agent_sdk_session", ROOT / "aic_agent_sdk_session", 8890),
    "aic_agent_sdk_worker": Service("aic_agent_sdk_worker", ROOT / "aic_agent_sdk_worker", None),
    "aic_agent_sdk_api": Service("aic_agent_sdk_api", ROOT / "aic_agent_sdk_api", 6789),
}


def default_config() -> dict[str, Any]:
    return {
        "version": CONFIG_VERSION,
        "workspace_root": DEFAULT_WORKSPACE_ROOT,
        "local_uid": 1234,
        "mysql_dsn": DEFAULT_MYSQL_DSN,
        "redis": {
            "addr": "127.0.0.1:6379",
            "password": "",
            "db": 0,
        },
        "model_env": {
            "mode": "file",
            "file": DEFAULT_ENV_FILE,
        },
        "worker_config": DEFAULT_WORKER_CONFIG,
        "agent_coordinator": {
            "mode": "direct",
            "namespace": "cloud_agent",
            "psm": "ad.creative.aic_agent_coordinator",
            "cluster": "",
            "hostports": ["127.0.0.1:8888"],
        },
        "ports": {
            "aic_agent_sdk_session": 8890,
            "aic_agent_sdk_api": 6789,
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="AIC Agent SDK local dev manager")
    parser.add_argument("--config", default=str(DEFAULT_CONFIG_PATH), help="dev config path")
    sub = parser.add_subparsers(dest="command")

    p_configure = sub.add_parser("configure", help="create or update local dev config")
    p_configure.add_argument("--defaults", action="store_true", help="write default config without prompts")
    p_configure.add_argument("--force", action="store_true", help="overwrite existing config")

    sub.add_parser("doctor", help="check local dependencies")
    sub.add_parser("init-db", help="create local MySQL tables if missing")
    sub.add_parser("start", help="start aic_agent_sdk_session, aic_agent_sdk_worker, aic_agent_sdk_api")
    sub.add_parser("stop", help="stop services started by this script")
    sub.add_parser("status", help="show service status")
    sub.add_parser("logs", help="show log paths and recent log tails")
    sub.add_parser("smoke", help="run a lightweight HTTP smoke test")

    args = parser.parse_args()
    config_path = Path(args.config).expanduser()

    if args.command is None:
        return menu(config_path)
    if args.command == "configure":
        return cmd_configure(config_path, defaults=args.defaults, force=args.force)

    cfg = load_or_configure(config_path)
    if args.command == "doctor":
        return cmd_doctor(cfg)
    if args.command == "init-db":
        return cmd_init_db(cfg)
    if args.command == "start":
        return cmd_start(cfg)
    if args.command == "stop":
        return cmd_stop()
    if args.command == "status":
        return cmd_status(cfg)
    if args.command == "logs":
        return cmd_logs()
    if args.command == "smoke":
        return cmd_smoke(cfg)
    parser.print_help()
    return 2


def menu(config_path: Path) -> int:
    while True:
        print()
        print("AIC Agent SDK Dev")
        print(f"Config: {config_path}")
        print("1. Configure local environment")
        print("2. Doctor / check dependencies")
        print("3. Init database tables")
        print("4. Start services")
        print("5. Stop services")
        print("6. Status")
        print("7. Logs")
        print("8. Smoke test")
        print("9. Exit")
        choice = input("Select: ").strip()
        if choice == "1":
            code = cmd_configure(config_path, defaults=False, force=True)
        elif choice == "2":
            code = cmd_doctor(load_or_configure(config_path))
        elif choice == "3":
            code = cmd_init_db(load_or_configure(config_path))
        elif choice == "4":
            code = cmd_start(load_or_configure(config_path))
        elif choice == "5":
            code = cmd_stop()
        elif choice == "6":
            code = cmd_status(load_or_configure(config_path))
        elif choice == "7":
            code = cmd_logs()
        elif choice == "8":
            code = cmd_smoke(load_or_configure(config_path))
        elif choice in {"9", "q", "quit", "exit"}:
            return 0
        else:
            print("Unknown choice.")
            continue
        if code != 0:
            print(f"Command failed with code {code}.")


def cmd_configure(config_path: Path, defaults: bool, force: bool) -> int:
    if config_path.exists() and not force:
        print(f"Config already exists: {config_path}")
        print("Use configure --force to overwrite it.")
        return 0

    cfg = default_config()
    if not defaults:
        print("Configure AIC Agent SDK local development.")
        cfg["workspace_root"] = prompt("Workspace root", cfg["workspace_root"])
        cfg["local_uid"] = int(prompt("Local uid", str(cfg["local_uid"])))
        cfg["mysql_dsn"] = prompt("MySQL DSN", cfg["mysql_dsn"])
        cfg["redis"]["addr"] = prompt("Redis address", cfg["redis"]["addr"])
        cfg["redis"]["password"] = prompt("Redis password", cfg["redis"]["password"], secret=True)
        cfg["redis"]["db"] = int(prompt("Redis db", str(cfg["redis"]["db"])))

        print()
        print("Model env source:")
        print("1. Current shell env")
        print("2. Env file")
        model_mode = prompt("Select", "2")
        if model_mode == "1":
            cfg["model_env"] = {"mode": "shell", "file": ""}
        else:
            cfg["model_env"] = {"mode": "file", "file": prompt("Env file", cfg["model_env"]["file"])}
        cfg["worker_config"] = prompt("Worker YAML", cfg["worker_config"])

        print()
        print("Agent Coordinator access:")
        print("1. Direct host:port, for local development")
        print("2. PSM + cluster, for BOE / online-like environment")
        ac_mode = prompt("Select", "1")
        if ac_mode == "2":
            cfg["agent_coordinator"]["mode"] = "psm"
            cfg["agent_coordinator"]["hostports"] = []
            cfg["agent_coordinator"]["cluster"] = prompt("AC cluster", cfg["agent_coordinator"]["cluster"] or "default")
        else:
            cfg["agent_coordinator"]["mode"] = "direct"
            hostports = prompt("AC hostports", ",".join(cfg["agent_coordinator"]["hostports"]))
            cfg["agent_coordinator"]["hostports"] = split_csv(hostports)
            cfg["agent_coordinator"]["cluster"] = ""
        cfg["agent_coordinator"]["namespace"] = prompt("AC namespace", cfg["agent_coordinator"]["namespace"])

    config_path.parent.mkdir(parents=True, exist_ok=True)
    config_path.write_text(json.dumps(cfg, indent=2, ensure_ascii=False) + "\n")
    print(f"Wrote {config_path}")
    return 0


def cmd_doctor(cfg: dict[str, Any]) -> int:
    checks: list[tuple[str, bool, str]] = []
    checks.append(("python", True, sys.version.split()[0]))
    checks.append(check_command("go", ["go", "version"]))
    checks.append(check_command("mysql", ["mysql", "--version"]))

    mysql = parse_mysql_dsn(cfg["mysql_dsn"])
    checks.append(check_tcp("mysql", mysql["host"], int(mysql["port"])))

    redis_host, redis_port = split_host_port(cfg["redis"]["addr"], 6379)
    checks.append(check_tcp("redis", redis_host, redis_port))

    ac = cfg["agent_coordinator"]
    if ac["mode"] == "direct":
        for hostport in ac.get("hostports", []):
            host, port = split_host_port(hostport, 8888)
            checks.append(check_tcp("agent_coordinator", host, port))
    else:
        ok = bool(ac.get("psm")) and bool(ac.get("cluster"))
        checks.append(("agent_coordinator psm+cluster", ok, f"psm={ac.get('psm')} cluster={ac.get('cluster')}"))

    checks.append(check_model_env(resolve_model_env(cfg)))
    worker_config = Path(cfg["worker_config"]).expanduser()
    checks.append(("worker config", worker_config.is_file(), str(worker_config)))

    ok = True
    for name, passed, detail in checks:
        status = "ok" if passed else "missing"
        print(f"[{status}] {name}: {detail}")
        ok = ok and passed
    return 0 if ok else 1


def cmd_init_db(cfg: dict[str, Any]) -> int:
    mysql = parse_mysql_dsn(cfg["mysql_dsn"])
    sql_files = [
        ROOT / "aic_agent_sdk_session" / "sql" / "t_agent_session.sql",
        REPO_ROOT / "cloudagent" / "worker" / "sql" / "t_agent_thread_ref.sql",
        REPO_ROOT / "cloudagent" / "worker" / "sql" / "t_agentthread_history.sql",
        REPO_ROOT / "deepagents" / "memory" / "gorm_store" / "sql" / "t_memory_source.sql",
        REPO_ROOT / "deepagents" / "memory" / "gorm_store" / "sql" / "t_memory_stage1_output.sql",
        REPO_ROOT / "deepagents" / "memory" / "gorm_store" / "sql" / "t_memory_stage2_job.sql",
        REPO_ROOT / "deepagents" / "memory" / "gorm_store" / "sql" / "t_memory_baseline.sql",
    ]
    ensure_database(mysql)
    for sql_file in sql_files:
        table = table_name_from_sql(sql_file.read_text())
        if not table:
            print(f"[skip] cannot find table name in {sql_file}")
            continue
        if table_exists(mysql, table):
            print(f"[ok] table exists: {table}")
        else:
            execute_mysql(mysql, sql_file.read_text())
            print(f"[created] table: {table}")
    ensure_session_columns(mysql)
    ensure_history_seq_columns(mysql)
    return 0


def cmd_start(cfg: dict[str, Any]) -> int:
    if cmd_doctor(cfg) != 0:
        print("Doctor failed. Fix missing dependencies first.")
        return 1
    if cmd_init_db(cfg) != 0:
        return 1
    GENERATED_DIR.mkdir(parents=True, exist_ok=True)
    BIN_DIR.mkdir(parents=True, exist_ok=True)
    PID_DIR.mkdir(parents=True, exist_ok=True)
    LOG_DIR.mkdir(parents=True, exist_ok=True)
    write_session_kitex_config(cfg)

    start_service(cfg, "aic_agent_sdk_session")
    wait_port("aic_agent_sdk_session", service_port(cfg, "aic_agent_sdk_session"))

    start_service(cfg, "aic_agent_sdk_worker")
    time.sleep(2)
    assert_process_alive("aic_agent_sdk_worker")

    start_service(cfg, "aic_agent_sdk_api")
    wait_http("aic_agent_sdk_api", f"http://127.0.0.1:{service_port(cfg, 'aic_agent_sdk_api')}/")
    print()
    print(f"AIC Agent SDK is ready: http://127.0.0.1:{service_port(cfg, 'aic_agent_sdk_api')}/")
    return 0


def cmd_stop() -> int:
    ok = True
    for name in ["aic_agent_sdk_api", "aic_agent_sdk_worker", "aic_agent_sdk_session"]:
        ok = stop_service(name) and ok
    return 0 if ok else 1


def cmd_status(cfg: dict[str, Any]) -> int:
    for name, service in SERVICES.items():
        pid = read_pid(name)
        pid_state = "no pid"
        if pid:
            pid_state = "running" if process_alive(pid) else f"dead pid={pid}"
        port_state = "no port"
        port = service_port(cfg, name)
        if port:
            port_state = "listening" if tcp_connect("127.0.0.1", port, 0.3) else "not listening"
        print(f"{name}: {pid_state}; {port_state}; log={log_path(name)}")
    return 0


def cmd_logs() -> int:
    for name in SERVICES:
        path = log_path(name)
        print()
        print(f"== {name}: {path}")
        if not path.exists():
            print("(missing)")
            continue
        tail_file(path, 30)
    return 0


def cmd_smoke(cfg: dict[str, Any]) -> int:
    api_port = service_port(cfg, "aic_agent_sdk_api")
    url = f"http://127.0.0.1:{api_port}/ad/aic_agent_sdk/list_projects"
    try:
        body = http_post_json(url, {})
    except Exception as exc:
        print(f"[failed] list_projects: {exc}")
        return 1
    print("[ok] list_projects")
    print(json.dumps(body, ensure_ascii=False, indent=2)[:2000])
    return 0


def start_service(cfg: dict[str, Any], name: str) -> None:
    service = SERVICES[name]
    existing = read_pid(name)
    if existing and process_alive(existing):
        print(f"[ok] {name} already running pid={existing}")
        return
    port = service_port(cfg, name)
    if port and tcp_connect("127.0.0.1", port, 0.2):
        raise SystemExit(f"{name} port {port} is already in use by an unknown process")

    env = service_env(cfg, name)
    build_service(name)
    command = service_command(cfg, name)
    path = log_path(name)
    path.parent.mkdir(parents=True, exist_ok=True)
    log_file = path.open("ab")
    proc = subprocess.Popen(
        command,
        cwd=service.cwd,
        env=env,
        stdout=log_file,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    write_pid(name, proc.pid)
    print(f"[started] {name} pid={proc.pid} log={path}")


def stop_service(name: str) -> bool:
    pid = read_pid(name)
    if not pid:
        print(f"[skip] {name}: no pid")
        return True
    if not process_alive(pid) and not process_group_alive(pid):
        print(f"[skip] {name}: pid {pid} is not running")
        pid_path(name).unlink(missing_ok=True)
        return True
    try:
        os.killpg(pid, signal.SIGTERM)
    except ProcessLookupError:
        pid_path(name).unlink(missing_ok=True)
        return True
    deadline = time.time() + 10
    while time.time() < deadline:
        if not process_group_alive(pid):
            pid_path(name).unlink(missing_ok=True)
            print(f"[stopped] {name}")
            return True
        time.sleep(0.2)
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pid_path(name).unlink(missing_ok=True)
        print(f"[stopped] {name}")
        return True
    deadline = time.time() + 5
    while time.time() < deadline:
        if not process_group_alive(pid):
            pid_path(name).unlink(missing_ok=True)
            print(f"[killed] {name}")
            return True
        time.sleep(0.2)
    print(f"[failed] {name}: still running process group pgid={pid}")
    return False


def service_command(cfg: dict[str, Any], name: str) -> list[str]:
    if name == "aic_agent_sdk_session":
        return [str(service_binary(name))]
    if name == "aic_agent_sdk_worker":
        return [str(service_binary(name))]
    if name == "aic_agent_sdk_api":
        return [
            str(service_binary(name)),
            "-psm=ad.creative.aic_agent_sdk_api",
            "-conf-dir=conf",
            f"-log-dir={LOG_DIR / 'hertz'}",
            f"-port={service_port(cfg, 'aic_agent_sdk_api')}",
        ]
    raise KeyError(name)


def service_binary(name: str) -> Path:
    if name not in SERVICES:
        raise KeyError(name)
    return BIN_DIR / name


def build_service(name: str) -> None:
    service = SERVICES[name]
    binary = service_binary(name)
    binary.parent.mkdir(parents=True, exist_ok=True)
    print(f"[build] {name}")
    subprocess.run(["go", "build", "-o", str(binary), "."], cwd=service.cwd, check=True)


def service_env(cfg: dict[str, Any], name: str) -> dict[str, str]:
    env = resolve_model_env(cfg)
    ac = cfg["agent_coordinator"]
    if name == "aic_agent_sdk_session":
        env["AIC_AGENT_SDK_SESSION_MYSQL_DSN"] = cfg["mysql_dsn"]
        env["AIC_AGENT_SDK_SESSION_AC_NAMESPACE"] = ac["namespace"]
        env["AIC_AGENT_SDK_SESSION_AC_PSM"] = ac["psm"]
        env["KITEX_CONF_DIR"] = str(GENERATED_DIR / "aic_agent_sdk_session_conf")
        if ac["mode"] == "direct":
            env["AIC_AGENT_SDK_SESSION_AC_HOSTPORTS"] = ",".join(ac.get("hostports", []))
            env.pop("AIC_AGENT_SDK_SESSION_AC_CLUSTER", None)
        else:
            env.pop("AIC_AGENT_SDK_SESSION_AC_HOSTPORTS", None)
            env["AIC_AGENT_SDK_SESSION_AC_CLUSTER"] = ac["cluster"]
    elif name == "aic_agent_sdk_api":
        env["AIC_AGENT_SDK_API_AUTH_MODE"] = "local"
        env["AIC_AGENT_SDK_API_LOCAL_DEFAULT_UID"] = str(cfg["local_uid"])
        env["AIC_AGENT_SDK_API_WORKSPACE_ROOT"] = cfg["workspace_root"]
        env["AIC_AGENT_SDK_API_BACKEND_TYPE"] = "local"
        env["AIC_AGENT_SDK_API_BACKEND_LOCAL_ROOT"] = cfg["workspace_root"]
        env["AIC_AGENT_SDK_API_AC_NAMESPACE"] = ac["namespace"]
        env["AIC_AGENT_SDK_API_SESSION_DIRECT_HOSTPORTS"] = f"127.0.0.1:{service_port(cfg, 'aic_agent_sdk_session')}"
        if ac["mode"] == "direct":
            env["AIC_AGENT_SDK_API_AC_DIRECT_HOSTPORTS"] = ",".join(ac.get("hostports", []))
            env.pop("AIC_AGENT_SDK_API_AC_CLUSTER", None)
        else:
            env.pop("AIC_AGENT_SDK_API_AC_DIRECT_HOSTPORTS", None)
            env["AIC_AGENT_SDK_API_AC_CLUSTER"] = ac["cluster"]
    elif name == "aic_agent_sdk_worker":
        env["AGENT_WORKER_CONF"] = str(Path(cfg["worker_config"]).expanduser())
        env["AGENT_WORKER_MYSQL_DSN"] = str(cfg["mysql_dsn"])
        env["AIC_AGENT_SDK_WORKSPACE_ROOT"] = str(cfg["workspace_root"])
    return env


def write_session_kitex_config(cfg: dict[str, Any]) -> None:
    port = service_port(cfg, "aic_agent_sdk_session")
    if port is None:
        raise SystemExit("aic_agent_sdk_session port is required")
    conf_dir = GENERATED_DIR / "aic_agent_sdk_session_conf"
    conf_dir.mkdir(parents=True, exist_ok=True)
    content = f"""Address: ":{port}"
LogLevel: info
DebugServerPort: "{port + 10000}"
"""
    (conf_dir / "kitex.yml").write_text(content)



def load_or_configure(config_path: Path) -> dict[str, Any]:
    if not config_path.exists():
        print(f"No config found: {config_path}")
        answer = input("Create default config now? [Y/n] ").strip().lower()
        if answer in {"", "y", "yes"}:
            cmd_configure(config_path, defaults=False, force=True)
        else:
            raise SystemExit("Config is required.")
    return load_config(config_path)


def load_config(config_path: Path) -> dict[str, Any]:
    cfg = json.loads(config_path.read_text())
    base = default_config()
    return deep_merge(base, cfg)


def deep_merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    out = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = deep_merge(out[key], value)
        else:
            out[key] = value
    return out


def prompt(label: str, default: str, secret: bool = False) -> str:
    suffix = " (hidden)" if secret and default else ""
    raw = input(f"{label}{suffix} [{default}]: ").strip()
    return raw if raw else default


def check_command(name: str, command: list[str]) -> tuple[str, bool, str]:
    if not shutil.which(command[0]):
        return name, False, f"{command[0]} not found in PATH"
    try:
        out = subprocess.run(command, check=False, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=5)
        return name, out.returncode == 0, out.stdout.strip().splitlines()[0] if out.stdout else "ok"
    except Exception as exc:
        return name, False, str(exc)


def check_tcp(name: str, host: str, port: int) -> tuple[str, bool, str]:
    ok = tcp_connect(host, port, 0.5)
    return name, ok, f"{host}:{port}"


def check_model_env(env: dict[str, str]) -> tuple[str, bool, str]:
    if env.get("OPENAI_API_KEY"):
        return "model env", True, "OPENAI_API_KEY"
    return "model env", False, "set OPENAI_API_KEY for the worker.local.yml model"


def build_model_env(cfg: dict[str, Any]) -> dict[str, str]:
    model = cfg.get("model_env", {})
    if model.get("mode") != "file":
        return {}
    path = Path(model.get("file", "")).expanduser()
    return parse_env_file(path)


def resolve_model_env(cfg: dict[str, Any]) -> dict[str, str]:
    env = os.environ.copy()
    model = cfg.get("model_env", {})
    if model.get("mode") == "file":
        for key in MODEL_PROVIDER_ENV_KEYS:
            env.pop(key, None)
    env.update(build_model_env(cfg))
    return env


def parse_env_file(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    values: dict[str, str] = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export ") :].strip()
        last_key = ""
        for token in shlex.split(line):
            if "=" not in token:
                if last_key == "FORNAX_SPACE_AK" and "FORNAX_SPACE_SK" not in values:
                    values["FORNAX_SPACE_SK"] = token.strip()
                continue
            key, value = token.split("=", 1)
            last_key = key.strip()
            values[last_key] = value.strip()
    return values


def ensure_database(mysql: dict[str, str]) -> None:
    args = mysql_args(mysql, with_database=False)
    subprocess.run(args + ["-e", f"CREATE DATABASE IF NOT EXISTS `{mysql['database']}` DEFAULT CHARSET utf8mb4"], check=True)


def table_exists(mysql: dict[str, str], table: str) -> bool:
    out = mysql_query(mysql, f"SHOW TABLES LIKE '{table}'")
    return table in out


def ensure_session_columns(mysql: dict[str, str]) -> None:
    columns = mysql_query(mysql, "SHOW COLUMNS FROM t_agent_session")
    if "project_name" not in columns:
        execute_mysql(mysql, "ALTER TABLE t_agent_session ADD COLUMN project_name VARCHAR(256) NOT NULL DEFAULT '' COMMENT 'project directory name under user workspace' AFTER uid")
        print("[altered] t_agent_session.project_name")
    if "project_path" not in columns:
        execute_mysql(mysql, "ALTER TABLE t_agent_session ADD COLUMN project_path VARCHAR(1024) NOT NULL DEFAULT '' COMMENT 'resolved worker cwd for this session' AFTER project_name")
        print("[altered] t_agent_session.project_path")
    indexes = mysql_query(mysql, "SHOW INDEX FROM t_agent_session")
    if "idx_uid_project_active" not in indexes:
        execute_mysql(mysql, "ALTER TABLE t_agent_session ADD KEY idx_uid_project_active (uid, project_name, status, last_active_at_ms)")
        print("[altered] t_agent_session.idx_uid_project_active")


def ensure_history_seq_columns(mysql: dict[str, str]) -> None:
    columns = mysql_query(mysql, "SHOW COLUMNS FROM t_agentthread_history")
    column_names = {line.split("\t", 1)[0] for line in columns.splitlines() if line}
    if "seq" not in column_names:
        execute_mysql(mysql, "ALTER TABLE t_agentthread_history ADD COLUMN seq BIGINT NOT NULL DEFAULT 0 COMMENT 'per-thread history append sequence' AFTER message_id")
        print("[altered] t_agentthread_history.seq")
    indexes = mysql_query(mysql, "SHOW INDEX FROM t_agentthread_history")
    if "idx_thread_seq" not in indexes:
        execute_mysql(mysql, "ALTER TABLE t_agentthread_history ADD KEY idx_thread_seq (thread_id, seq)")
        print("[altered] t_agentthread_history.idx_thread_seq")
    if "idx_thread_turn_seq" not in indexes:
        execute_mysql(mysql, "ALTER TABLE t_agentthread_history ADD KEY idx_thread_turn_seq (thread_id, turn_id, seq)")
        print("[altered] t_agentthread_history.idx_thread_turn_seq")


def execute_mysql(mysql: dict[str, str], sql: str) -> None:
    subprocess.run(mysql_args(mysql), input=sql, text=True, check=True)


def mysql_query(mysql: dict[str, str], sql: str) -> str:
    out = subprocess.run(mysql_args(mysql) + ["-N", "-e", sql], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    return out.stdout


def mysql_args(mysql: dict[str, str], with_database: bool = True) -> list[str]:
    args = ["mysql", f"-h{mysql['host']}", f"-P{mysql['port']}", f"-u{mysql['user']}"]
    if mysql["password"]:
        args.append(f"-p{mysql['password']}")
    if with_database:
        args.append(mysql["database"])
    return args


def parse_mysql_dsn(dsn: str) -> dict[str, str]:
    pattern = re.compile(r"(?P<user>[^:]+):(?P<password>[^@]*)@tcp\((?P<host>[^:)]+):(?P<port>\d+)\)/(?P<database>[^?]+)")
    match = pattern.match(dsn)
    if not match:
        raise SystemExit(f"Unsupported MySQL DSN format: {dsn}")
    return match.groupdict()


def table_name_from_sql(sql: str) -> str:
    match = re.search(r"CREATE\s+TABLE\s+`?([A-Za-z0-9_]+)`?", sql, re.IGNORECASE)
    return match.group(1) if match else ""


def wait_port(name: str, port: int) -> None:
    deadline = time.time() + 45
    while time.time() < deadline:
        assert_process_alive(name)
        if tcp_connect("127.0.0.1", port, 0.3):
            print(f"[ready] {name} port={port}")
            return
        time.sleep(0.5)
    fail_with_log(name, f"{name} did not listen on port {port}")


def wait_http(name: str, url: str) -> None:
    deadline = time.time() + 60
    while time.time() < deadline:
        assert_process_alive(name)
        try:
            with urllib.request.urlopen(url, timeout=1) as resp:
                if 200 <= resp.status < 500:
                    print(f"[ready] {name} {url}")
                    return
        except Exception:
            pass
        time.sleep(0.5)
    fail_with_log(name, f"{name} did not become healthy: {url}")


def assert_process_alive(name: str) -> None:
    pid = read_pid(name)
    if not pid or not process_alive(pid):
        fail_with_log(name, f"{name} process exited")


def fail_with_log(name: str, message: str) -> None:
    print(f"[failed] {message}")
    path = log_path(name)
    if path.exists():
        print(f"Last log lines from {path}:")
        tail_file(path, 80)
    raise SystemExit(1)


def tcp_connect(host: str, port: int, timeout: float) -> bool:
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except OSError:
        return False


def http_post_json(url: str, payload: dict[str, Any]) -> dict[str, Any]:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=5) as resp:
        return json.loads(resp.read().decode("utf-8"))


def process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except OSError:
        return False


def process_group_alive(pgid: int) -> bool:
    try:
        os.killpg(pgid, 0)
        return True
    except OSError:
        return False


def read_pid(name: str) -> int | None:
    path = pid_path(name)
    if not path.exists():
        return None
    try:
        return int(path.read_text().strip())
    except ValueError:
        return None


def write_pid(name: str, pid: int) -> None:
    PID_DIR.mkdir(parents=True, exist_ok=True)
    pid_path(name).write_text(f"{pid}\n")


def pid_path(name: str) -> Path:
    return PID_DIR / f"{name}.pid"


def log_path(name: str) -> Path:
    return LOG_DIR / f"{name}.log"


def service_port(cfg: dict[str, Any], name: str) -> int | None:
    if name == "aic_agent_sdk_api":
        return int(cfg["ports"]["aic_agent_sdk_api"])
    if name == "aic_agent_sdk_session":
        return int(cfg["ports"]["aic_agent_sdk_session"])
    return None


def tail_file(path: Path, lines: int) -> None:
    data = path.read_text(errors="replace").splitlines()
    for line in data[-lines:]:
        print(line)


def split_csv(value: str) -> list[str]:
    return [part.strip() for part in value.split(",") if part.strip()]


def split_host_port(hostport: str, default_port: int) -> tuple[str, int]:
    if ":" not in hostport:
        return hostport, default_port
    host, port = hostport.rsplit(":", 1)
    return host, int(port)


if __name__ == "__main__":
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(line_buffering=True)
    raise SystemExit(main())
