#!/usr/bin/env python3
"""Audit local CloudAgent MySQL rows and Redis runtime state."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


TABLES = (
    "t_agent_namespace",
    "t_thread",
    "t_mailbox_message",
    "t_event_log",
    "t_agent_session",
    "t_agent_thread_ref",
    "t_agentthread_history",
    "t_memory_source",
    "t_memory_stage1_output",
    "t_memory_stage2_job",
    "t_memory_baseline",
)


class CommandError(RuntimeError):
    pass


def run(command: list[str], *, env: dict[str, str] | None = None, stdin: str | None = None) -> str:
    result = subprocess.run(
        command,
        input=stdin,
        text=True,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
    )
    if result.returncode != 0:
        raise CommandError(result.stderr.strip() or f"command exited with code {result.returncode}")
    return result.stdout


class MySQL:
    def __init__(self, database: str):
        self.database = database
        self.env = os.environ.copy()
        self.env["MYSQL_PWD"] = os.environ.get(
            "MYSQL_PASSWORD", os.environ.get("MYSQL_PWD", "ac_test_pwd_20260416")
        )
        self.command = [
            "mysql",
            "-h" + os.environ.get("MYSQL_HOST", "127.0.0.1"),
            "-P" + os.environ.get("MYSQL_PORT", "3306"),
            "-u" + os.environ.get("MYSQL_USER", "ac_test"),
            "-N",
            "-B",
            database,
        ]

    def rows(self, sql: str) -> list[list[str]]:
        output = run(self.command, env=self.env, stdin=sql)
        return [line.split("\t") for line in output.splitlines() if line]

    def scalar(self, sql: str) -> int:
        rows = self.rows(sql)
        return int(rows[0][0]) if rows else 0


class Redis:
    def __init__(self):
        addr = os.environ.get("REDIS_ADDR", "127.0.0.1:6379")
        host, separator, port = addr.rpartition(":")
        if not separator:
            host, port = addr, "6379"
        self.command = ["redis-cli", "-h", host, "-p", port, "-n", os.environ.get("REDIS_DB", "0")]
        self.env = os.environ.copy()
        if os.environ.get("REDIS_PASSWORD"):
            self.env["REDISCLI_AUTH"] = os.environ["REDIS_PASSWORD"]

    def call(self, *args: str) -> str:
        return run(self.command + ["--raw", *args], env=self.env).rstrip("\n")

    def scan(self, pattern: str) -> list[str]:
        return [key for key in self.call("--scan", "--pattern", pattern).splitlines() if key]


def named_counts(mysql: MySQL, checks: dict[str, str]) -> dict[str, int]:
    return {name: mysql.scalar(sql) for name, sql in checks.items()}


def mysql_report(mysql: MySQL) -> dict[str, Any]:
    row_counts = {table: mysql.scalar(f"SELECT COUNT(*) FROM `{table}`") for table in TABLES}
    invalid_json = named_counts(mysql, {
        "namespace.metadata_json": "SELECT COUNT(*) FROM t_agent_namespace WHERE NOT JSON_VALID(metadata_json)",
        "thread.metadata_json": "SELECT COUNT(*) FROM t_thread WHERE NOT JSON_VALID(metadata_json)",
        "thread.profile": "SELECT COUNT(*) FROM t_thread WHERE profile IS NOT NULL AND NOT JSON_VALID(profile)",
        "mailbox.metadata_json": "SELECT COUNT(*) FROM t_mailbox_message WHERE NOT JSON_VALID(metadata_json)",
        "event.metadata_json": "SELECT COUNT(*) FROM t_event_log WHERE NOT JSON_VALID(metadata_json)",
        "session.metadata_json": "SELECT COUNT(*) FROM t_agent_session WHERE NOT JSON_VALID(metadata_json)",
        "history.message": "SELECT COUNT(*) FROM t_agentthread_history WHERE COALESCE(message,'')<>'' AND NOT JSON_VALID(message)",
        "history.ext": "SELECT COUNT(*) FROM t_agentthread_history WHERE COALESCE(ext,'')<>'' AND NOT JSON_VALID(ext)",
    })
    orphans = named_counts(mysql, {
        "thread.namespace": "SELECT COUNT(*) FROM t_thread t LEFT JOIN t_agent_namespace n ON n.namespace=t.namespace WHERE n.namespace IS NULL",
        "mailbox.thread": "SELECT COUNT(*) FROM t_mailbox_message m LEFT JOIN t_thread t ON t.thread_id=m.thread_id WHERE t.thread_id IS NULL",
        "event.thread": "SELECT COUNT(*) FROM t_event_log e LEFT JOIN t_thread t ON t.thread_id=e.thread_id WHERE t.thread_id IS NULL",
        "session.main_thread": "SELECT COUNT(*) FROM t_agent_session s LEFT JOIN t_thread t ON t.thread_id=s.main_thread_id WHERE s.main_thread_id<>0 AND t.thread_id IS NULL",
        "thread_ref.thread": "SELECT COUNT(*) FROM t_agent_thread_ref r LEFT JOIN t_thread t ON t.thread_id=r.thread_id WHERE t.thread_id IS NULL",
        "thread_ref.session": "SELECT COUNT(*) FROM t_agent_thread_ref r LEFT JOIN t_agent_session s ON CONVERT(s.session_id USING utf8mb4)=CONVERT(r.session_id USING utf8mb4) WHERE s.session_id IS NULL",
        "history.thread": "SELECT COUNT(*) FROM t_agentthread_history h LEFT JOIN t_thread t ON t.thread_id=h.thread_id WHERE t.thread_id IS NULL",
        "event.thread_owner": "SELECT COUNT(*) FROM t_event_log e JOIN t_thread t ON t.thread_id=e.thread_id WHERE e.namespace<>t.namespace OR (e.session_id<>'' AND e.session_id<>t.session_id)",
    })
    invalid_values = named_counts(mysql, {
        "thread.status": "SELECT COUNT(*) FROM t_thread WHERE status NOT IN ('idle','ready','running','blocked','closing','closed')",
        "mailbox.status": "SELECT COUNT(*) FROM t_mailbox_message WHERE status NOT IN ('pending','acked','canceled')",
        "session.status": "SELECT COUNT(*) FROM t_agent_session WHERE status NOT IN (1,2,3)",
        "history.type": "SELECT COUNT(*) FROM t_agentthread_history WHERE type NOT IN ('message','compact')",
    })
    inconsistent_state = named_counts(mysql, {
        "thread.closed_at": "SELECT COUNT(*) FROM t_thread WHERE (status='closed')<>(closed_at IS NOT NULL)",
        "thread.ready_at": "SELECT COUNT(*) FROM t_thread WHERE status='ready' AND ready_at IS NULL",
        "mailbox.handled_at": "SELECT COUNT(*) FROM t_mailbox_message WHERE (status='pending' AND handled_at IS NOT NULL) OR (status<>'pending' AND handled_at IS NULL)",
        "session.closed_at_ms": "SELECT COUNT(*) FROM t_agent_session WHERE (status=3)<>(closed_at_ms>0)",
    })
    future_timestamps = named_counts(mysql, {
        "thread": "SELECT COUNT(*) FROM t_thread WHERE created_at>NOW()+INTERVAL 5 MINUTE OR updated_at>NOW()+INTERVAL 5 MINUTE",
        "mailbox": "SELECT COUNT(*) FROM t_mailbox_message WHERE created_at>NOW()+INTERVAL 5 MINUTE OR updated_at>NOW()+INTERVAL 5 MINUTE",
        "event": "SELECT COUNT(*) FROM t_event_log WHERE created_at>NOW()+INTERVAL 5 MINUTE",
        "session": "SELECT COUNT(*) FROM t_agent_session WHERE created_at_ms>(UNIX_TIMESTAMP(NOW())+300)*1000 OR updated_at_ms>(UNIX_TIMESTAMP(NOW())+300)*1000",
        "history": "SELECT COUNT(*) FROM t_agentthread_history WHERE created_at>UNIX_TIMESTAMP(NOW())+300",
    })
    stale_rows = mysql.rows("""
SELECT thread_id, namespace, status, COALESCE(status_reason,''), DATE_FORMAT(updated_at,'%Y-%m-%dT%H:%i:%s')
FROM t_thread
WHERE status IN ('running','blocked','closing') AND updated_at < NOW()-INTERVAL 1 HOUR
ORDER BY updated_at, thread_id;
""")
    compatibility = {
        "mailbox_null_trigger_turn_id": mysql.scalar("SELECT COUNT(*) FROM t_mailbox_message WHERE status='acked' AND trigger_turn_id IS NULL"),
        "event_default_in_thread_seq": mysql.scalar("SELECT COUNT(*) FROM t_event_log WHERE in_thread_seq=0"),
    }
    pending_rows = mysql.rows("SELECT message_id, thread_id FROM t_mailbox_message WHERE status='pending'")
    return {
        "row_counts": row_counts,
        "invalid_json": invalid_json,
        "orphans": orphans,
        "invalid_values": invalid_values,
        "inconsistent_state": inconsistent_state,
        "future_timestamps": future_timestamps,
        "stale_nonterminal_threads": [
            {"thread_id": int(row[0]), "namespace": row[1], "status": row[2], "reason": row[3], "updated_at": row[4]}
            for row in stale_rows
        ],
        "compatibility_defaults": compatibility,
        "pending_messages": {int(row[0]): int(row[1]) for row in pending_rows},
    }


def redis_report(redis: Redis, mysql_messages: dict[int, tuple[int, str]], mysql_pending: dict[int, int]) -> dict[str, Any]:
    message_keys = redis.scan("ac:message:*")
    messages: dict[int, dict[str, Any]] = {}
    invalid_messages: list[dict[str, str]] = []
    for key in message_keys:
        raw = redis.call("GET", key)
        if not raw:
            continue
        try:
            value = json.loads(raw)
            messages[int(value["message_id"])] = value
        except (KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
            invalid_messages.append({"key": key, "error": str(error)})

    pending_members: dict[int, int] = {}
    pending_errors: list[str] = []
    input_pattern = re.compile(r"^ac:(.+):thread:(\d+):input$")
    for key in redis.scan("ac:*:thread:*:input"):
        match = input_pattern.match(key)
        if not match:
            pending_errors.append(f"unrecognized pending key: {key}")
            continue
        namespace, thread_text = match.groups()
        thread_id = int(thread_text)
        for member in redis.call("ZRANGE", key, "0", "-1").splitlines():
            try:
                message_id = int(member)
            except ValueError:
                pending_errors.append(f"non-numeric member {member!r} in {key}")
                continue
            pending_members[message_id] = thread_id
            cached = messages.get(message_id)
            if cached is None:
                pending_errors.append(f"pending message {message_id} has no cache body")
            elif cached.get("status") != "pending" or int(cached.get("thread_id", 0)) != thread_id or cached.get("namespace") != namespace:
                pending_errors.append(f"pending message {message_id} cache ownership/status mismatch")

    for message_id, thread_id in mysql_pending.items():
        if pending_members.get(message_id) != thread_id:
            pending_errors.append(f"MySQL pending message {message_id} missing from Redis input queue")
    for message_id, thread_id in pending_members.items():
        if mysql_pending.get(message_id) != thread_id:
            pending_errors.append(f"Redis pending message {message_id} missing from MySQL pending rows")

    live_body_count = 0
    live_body_without_ttl: list[str] = []
    live_body_pattern = re.compile(r"^ac:.+:session:.+:live:\d+$")
    for key in redis.scan("ac:*:session:*:live:*"):
        if not live_body_pattern.match(key):
            continue
        ttl = int(redis.call("TTL", key) or "-2")
        if ttl == -2:
            continue
        live_body_count += 1
        if ttl < 0:
            live_body_without_ttl.append(key)

    redis_only = sorted(set(messages) - set(mysql_messages))
    mysql_only = sorted(set(mysql_messages) - set(messages))
    return {
        "message_cache_count": len(messages),
        "invalid_message_cache": invalid_messages,
        "pending_queue_member_count": len(pending_members),
        "pending_queue_errors": pending_errors,
        "redis_only_messages": [
            {
                "message_id": message_id,
                "thread_id": int(messages[message_id].get("thread_id", 0)),
                "namespace": messages[message_id].get("namespace", ""),
                "status": messages[message_id].get("status", ""),
            }
            for message_id in redis_only
        ],
        "mysql_only_message_ids": mysql_only,
        "live_body_count": live_body_count,
        "live_body_without_ttl": live_body_without_ttl,
    }


def sum_counts(values: dict[str, int]) -> int:
    return sum(values.values())


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", default=os.environ.get("MYSQL_DB", "agent_coordinator_test"))
    parser.add_argument("--json-out", default="")
    parser.add_argument(
        "--cleanup-redis-only-acked",
        action="store_true",
        help="delete terminal Redis message caches that have no MySQL row",
    )
    args = parser.parse_args()

    mysql = MySQL(args.database)
    mysql_data = mysql_report(mysql)
    message_rows = mysql.rows("SELECT message_id, thread_id, status FROM t_mailbox_message")
    mysql_messages = {int(row[0]): (int(row[1]), row[2]) for row in message_rows}
    redis = Redis()
    redis_data = redis_report(redis, mysql_messages, mysql_data["pending_messages"])
    if args.cleanup_redis_only_acked:
        cleanup_ids = [
            item["message_id"] for item in redis_data["redis_only_messages"] if item["status"] == "acked"
        ]
        if cleanup_ids:
            redis.call("DEL", *(f"ac:message:{message_id}" for message_id in cleanup_ids))
            print(f"cleaned Redis-only acked message caches: {len(cleanup_ids)}")
            redis_data = redis_report(redis, mysql_messages, mysql_data["pending_messages"])
    mysql_data.pop("pending_messages")

    blockers = (
        sum_counts(mysql_data["invalid_json"])
        + sum_counts(mysql_data["orphans"])
        + sum_counts(mysql_data["invalid_values"])
        + sum_counts(mysql_data["inconsistent_state"])
        + sum_counts(mysql_data["future_timestamps"])
        + len(redis_data["invalid_message_cache"])
        + len(redis_data["pending_queue_errors"])
        + len(redis_data["live_body_without_ttl"])
    )
    warnings = (
        len(mysql_data["stale_nonterminal_threads"])
        + len(redis_data["redis_only_messages"])
        + len(redis_data["mysql_only_message_ids"])
    )
    report = {
        "database": args.database,
        "blocking_finding_count": blockers,
        "warning_finding_count": warnings,
        "mysql": mysql_data,
        "redis": redis_data,
    }
    if args.json_out:
        output = Path(args.json_out).expanduser()
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n")

    print(f"data audit: database={args.database} blocking={blockers} warnings={warnings}")
    print("row counts: " + ", ".join(f"{key}={value}" for key, value in mysql_data["row_counts"].items()))
    print(
        "redis: "
        f"message_cache={redis_data['message_cache_count']} "
        f"pending={redis_data['pending_queue_member_count']} "
        f"redis_only={len(redis_data['redis_only_messages'])} "
        f"mysql_only={len(redis_data['mysql_only_message_ids'])} "
        f"live_bodies={redis_data['live_body_count']}"
    )
    if mysql_data["stale_nonterminal_threads"]:
        print("[warning] stale nonterminal threads: " + json.dumps(mysql_data["stale_nonterminal_threads"], ensure_ascii=False))
    if any(mysql_data["compatibility_defaults"].values()):
        print("[info] historical compatibility defaults: " + json.dumps(mysql_data["compatibility_defaults"], ensure_ascii=False))
    if redis_data["redis_only_messages"]:
        print("[warning] Redis-only message cache: " + json.dumps(redis_data["redis_only_messages"], ensure_ascii=False))
    if redis_data["mysql_only_message_ids"]:
        print("[warning] MySQL-only message IDs: " + json.dumps(redis_data["mysql_only_message_ids"]))
    if blockers:
        print("data audit FAILED; inspect JSON report for blocking details", file=sys.stderr)
        return 1
    print("data audit PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
