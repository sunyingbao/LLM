#!/usr/bin/env python3

import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import dev
import audit_local_schema


class ModelEnvironmentTest(unittest.TestCase):
    def test_file_mode_is_authoritative_over_shell_provider_variables(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            env_file = Path(directory) / "model.env"
            env_file.write_text(
                "OPENAI_API_KEY=file-token\n"
                "OPENAI_BASE_URL=https://super-relay.byted.org/v1\n"
                "OPENAI_MODEL=model_api/experimental_0630\n"
            )
            cfg = dev.default_config()
            cfg["model_env"] = {"mode": "file", "file": str(env_file)}

            with patch.dict(
                os.environ,
                {
                    "KIMI_API_KEY": "shell-kimi-token",
                    "KIMI_MODEL": "shell-kimi-model",
                    "OPENROUTER_API_KEY": "shell-openrouter-token",
                },
                clear=True,
            ):
                env = dev.resolve_model_env(cfg)

            self.assertNotIn("KIMI_API_KEY", env)
            self.assertNotIn("KIMI_MODEL", env)
            self.assertNotIn("OPENROUTER_API_KEY", env)
            self.assertEqual(env["OPENAI_API_KEY"], "file-token")
            self.assertEqual(env["OPENAI_BASE_URL"], "https://super-relay.byted.org/v1")
            self.assertEqual(env["OPENAI_MODEL"], "model_api/experimental_0630")

    def test_shell_mode_preserves_parent_provider_variables(self) -> None:
        cfg = dev.default_config()
        cfg["model_env"] = {"mode": "shell", "file": ""}

        with patch.dict(os.environ, {"KIMI_API_KEY": "shell-token"}, clear=True):
            env = dev.resolve_model_env(cfg)

        self.assertEqual(env["KIMI_API_KEY"], "shell-token")


class DatabaseSchemaTest(unittest.TestCase):
    def test_all_database_schema_files_exist(self) -> None:
        sql_files = dev.database_sql_files()

        self.assertEqual(len(sql_files), 11)
        self.assertTrue(all(sql_file.is_file() for sql_file in sql_files))
        self.assertIn(
            dev.REPO_ROOT / "cloud" / "worker" / "sql" / "t_agent_thread_ref.sql",
            sql_files,
        )
        self.assertIn(
            dev.REPO_ROOT / "core" / "memory" / "gorm_store" / "sql" / "t_memory_source.sql",
            sql_files,
        )

    def test_all_audit_schema_files_exist(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            sql_files = audit_local_schema.ddl_files()

            self.assertEqual(len(sql_files), 11)
            self.assertTrue(all(sql_file.is_file() for sql_file in sql_files))


class CloudAgentServiceTest(unittest.TestCase):
    def test_command_does_not_require_psm(self) -> None:
        cfg = dev.default_config()

        self.assertEqual(
            dev.service_command(cfg, "cloud_agent"),
            [str(dev.service_binary("cloud_agent"))],
        )

    def test_address_is_provided_through_environment(self) -> None:
        cfg = dev.default_config()
        cfg["ports"]["cloud_agent"] = 8080

        with patch.object(dev, "resolve_model_env", return_value={}):
            env = dev.service_env(cfg, "cloud_agent")

        self.assertEqual(env["DEEP_AGENT_SDK_API_ADDRESS"], "127.0.0.1:8080")
        self.assertNotIn("PSM", env)


if __name__ == "__main__":
    unittest.main()
