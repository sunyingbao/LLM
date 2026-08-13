#!/usr/bin/env python3

import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import dev


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


if __name__ == "__main__":
    unittest.main()
