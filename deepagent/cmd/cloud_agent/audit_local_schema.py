#!/usr/bin/env python3
"""Compare the local CloudAgent MySQL schema with the current repository DDL."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent
SDK_REPO = ROOT.parents[1]


def ddl_files() -> list[Path]:
    return [
        SDK_REPO / "coordinator" / "sql" / "t_agent_namespace.sql",
        SDK_REPO / "coordinator" / "sql" / "t_thread.sql",
        SDK_REPO / "coordinator" / "sql" / "t_mailbox_message.sql",
        SDK_REPO / "coordinator" / "sql" / "t_event_log.sql",
        SDK_REPO / "cmd" / "cloud_agent" / "deep_agent_sdk_session" / "sql" / "t_agent_session.sql",
        SDK_REPO / "cloud" / "worker" / "sql" / "t_agent_thread_ref.sql",
        SDK_REPO / "cloud" / "worker" / "sql" / "t_agentthread_history.sql",
        SDK_REPO / "core" / "memory" / "gorm_store" / "sql" / "t_memory_source.sql",
        SDK_REPO / "core" / "memory" / "gorm_store" / "sql" / "t_memory_stage1_output.sql",
        SDK_REPO / "core" / "memory" / "gorm_store" / "sql" / "t_memory_stage2_job.sql",
        SDK_REPO / "core" / "memory" / "gorm_store" / "sql" / "t_memory_baseline.sql",
    ]


class MySQL:
    def __init__(self, database: str):
        self.host = os.environ.get("MYSQL_HOST", "127.0.0.1")
        self.port = os.environ.get("MYSQL_PORT", "3306")
        self.user = os.environ.get("MYSQL_USER", "ac_test")
        self.password = os.environ.get("MYSQL_PASSWORD", os.environ.get("MYSQL_PWD", "ac_test_pwd_20260416"))
        self.database = database

    def args(self, database: str | None = None) -> list[str]:
        args = ["mysql", f"-h{self.host}", f"-P{self.port}", f"-u{self.user}", "-N", "-B"]
        selected = self.database if database is None else database
        if selected:
            args.append(selected)
        return args

    def run(self, sql: str, database: str | None = None) -> str:
        env = os.environ.copy()
        env["MYSQL_PWD"] = self.password
        result = subprocess.run(
            self.args(database),
            input=sql,
            text=True,
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
        )
        if result.returncode != 0:
            raise RuntimeError(result.stderr.strip() or f"mysql exited with code {result.returncode}")
        return result.stdout


def parse_rows(raw: str) -> list[list[str]]:
    return [line.split("\t") for line in raw.splitlines() if line]


def table_names(files: list[Path]) -> list[str]:
    names: list[str] = []
    for path in files:
        match = re.search(r"CREATE\s+TABLE\s+`?([A-Za-z0-9_]+)`?", path.read_text(), re.IGNORECASE)
        if not match:
            raise SystemExit(f"cannot parse table name from {path}")
        names.append(match.group(1))
    return names


def quoted(values: list[str]) -> str:
    return ",".join("'" + value.replace("'", "''") + "'" for value in values)


def load_columns(mysql: MySQL, schema: str, tables: list[str]) -> dict[str, dict[str, tuple[Any, ...]]]:
    sql = f"""
SELECT table_name, column_name, ordinal_position, column_type, is_nullable,
       IF(column_default IS NULL, '1', '0'), HEX(COALESCE(column_default, '')),
       extra, COALESCE(character_set_name, ''), COALESCE(collation_name, '')
FROM information_schema.columns
WHERE table_schema='{schema.replace("'", "''")}' AND table_name IN ({quoted(tables)})
ORDER BY table_name, ordinal_position;
"""
    out: dict[str, dict[str, tuple[Any, ...]]] = {}
    for row in parse_rows(mysql.run(sql, database="")):
        table, name = row[0], row[1]
        default = None if row[5] == "1" else bytes.fromhex(row[6]).decode("utf-8")
        out.setdefault(table, {})[name] = (
            int(row[2]),
            row[3].lower(),
            row[4],
            default,
            row[7].lower(),
            row[8].lower(),
            row[9].lower(),
        )
    return out


def load_indexes(mysql: MySQL, schema: str, tables: list[str]) -> dict[str, dict[str, tuple[Any, ...]]]:
    sql = f"""
SELECT table_name, index_name, non_unique, seq_in_index, column_name,
       COALESCE(sub_part, 0), index_type
FROM information_schema.statistics
WHERE table_schema='{schema.replace("'", "''")}' AND table_name IN ({quoted(tables)})
ORDER BY table_name, index_name, seq_in_index;
"""
    grouped: dict[str, dict[str, list[tuple[Any, ...]]]] = {}
    for row in parse_rows(mysql.run(sql, database="")):
        grouped.setdefault(row[0], {}).setdefault(row[1], []).append(
            (int(row[2]), int(row[3]), row[4], int(row[5]), row[6].upper())
        )
    return {
        table: {name: tuple(parts) for name, parts in indexes.items()}
        for table, indexes in grouped.items()
    }


def load_tables(mysql: MySQL, schema: str, tables: list[str]) -> dict[str, tuple[str, str, str]]:
    sql = f"""
SELECT table_name, engine, table_collation, row_format
FROM information_schema.tables
WHERE table_schema='{schema.replace("'", "''")}' AND table_name IN ({quoted(tables)});
"""
    return {
        row[0]: (row[1].upper(), row[2].lower(), row[3].upper())
        for row in parse_rows(mysql.run(sql, database=""))
    }


def remap_tables(values: dict[str, Any], physical_to_logical: dict[str, str]) -> dict[str, Any]:
    return {physical_to_logical.get(table, table): value for table, value in values.items()}


def compare(schema: str, mysql: MySQL, tables: list[str], shadow_tables: dict[str, str]) -> dict[str, Any]:
    physical_to_logical = {physical: logical for logical, physical in shadow_tables.items()}
    physical_names = list(physical_to_logical)
    expected_tables = remap_tables(load_tables(mysql, schema, physical_names), physical_to_logical)
    actual_tables = load_tables(mysql, schema, tables)
    expected_columns = remap_tables(load_columns(mysql, schema, physical_names), physical_to_logical)
    actual_columns = load_columns(mysql, schema, tables)
    expected_indexes = remap_tables(load_indexes(mysql, schema, physical_names), physical_to_logical)
    actual_indexes = load_indexes(mysql, schema, tables)

    findings: dict[str, list[dict[str, Any]]] = {
        "missing_tables": [],
        "table_option_mismatches": [],
        "missing_columns": [],
        "column_mismatches": [],
        "extra_columns": [],
        "missing_indexes": [],
        "index_mismatches": [],
        "extra_indexes": [],
    }
    for table in tables:
        if table not in actual_tables:
            findings["missing_tables"].append({"table": table})
            continue
        if expected_tables[table] != actual_tables[table]:
            findings["table_option_mismatches"].append(
                {"table": table, "expected": expected_tables[table], "actual": actual_tables[table]}
            )

        expected_table_columns = expected_columns.get(table, {})
        actual_table_columns = actual_columns.get(table, {})
        for name, definition in expected_table_columns.items():
            if name not in actual_table_columns:
                findings["missing_columns"].append({"table": table, "column": name, "expected": definition})
            elif definition != actual_table_columns[name]:
                findings["column_mismatches"].append(
                    {"table": table, "column": name, "expected": definition, "actual": actual_table_columns[name]}
                )
        for name in sorted(set(actual_table_columns) - set(expected_table_columns)):
            findings["extra_columns"].append({"table": table, "column": name})

        expected_table_indexes = expected_indexes.get(table, {})
        actual_table_indexes = actual_indexes.get(table, {})
        for name, definition in expected_table_indexes.items():
            if name not in actual_table_indexes:
                findings["missing_indexes"].append({"table": table, "index": name, "expected": definition})
            elif definition != actual_table_indexes[name]:
                findings["index_mismatches"].append(
                    {"table": table, "index": name, "expected": definition, "actual": actual_table_indexes[name]}
                )
        for name in sorted(set(actual_table_indexes) - set(expected_table_indexes)):
            findings["extra_indexes"].append({"table": table, "index": name})
    return findings


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", default=os.environ.get("MYSQL_DB", "agent_coordinator_test"))
    parser.add_argument("--json-out", default="")
    args = parser.parse_args()

    files = ddl_files()
    for path in files:
        if not path.is_file():
            raise SystemExit(f"DDL file missing: {path}")
    tables = table_names(files)
    mysql = MySQL(args.database)
    shadow_prefix = f"_caa_{os.getpid()}_{int(time.time()) % 1_000_000}_"
    shadow_tables = {table: shadow_prefix + table for table in tables}
    created: list[str] = []
    try:
        for path, table in zip(files, tables):
            sql = re.sub(
                r"CREATE\s+TABLE\s+`?" + re.escape(table) + r"`?",
                f"CREATE TABLE `{shadow_tables[table]}`",
                path.read_text(),
                count=1,
                flags=re.IGNORECASE,
            )
            mysql.run(sql)
            created.append(shadow_tables[table])
        findings = compare(args.database, mysql, tables, shadow_tables)
    finally:
        for physical in reversed(created):
            mysql.run(f"DROP TABLE IF EXISTS `{physical}`")

    blocking_keys = (
        "missing_tables",
        "table_option_mismatches",
        "missing_columns",
        "column_mismatches",
        "missing_indexes",
        "index_mismatches",
    )
    blocking = sum(len(findings[key]) for key in blocking_keys)
    extras = len(findings["extra_columns"]) + len(findings["extra_indexes"])
    report = {
        "database": args.database,
        "ddl_tables": tables,
        "blocking_finding_count": blocking,
        "extra_finding_count": extras,
        "findings": findings,
    }
    if args.json_out:
        output = Path(args.json_out).expanduser()
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n")

    print(f"schema audit: database={args.database} tables={len(tables)} blocking={blocking} extras={extras}")
    for key, values in findings.items():
        if values:
            print(f"[{key}] count={len(values)}")
            for value in values:
                print("  " + json.dumps(value, ensure_ascii=False, default=str))
    if blocking:
        print("schema audit FAILED", file=sys.stderr)
        return 1
    print("schema audit PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
