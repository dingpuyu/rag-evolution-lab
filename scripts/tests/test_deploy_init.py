from __future__ import annotations

import importlib.util
import stat
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("deploy_init", ROOT / "scripts/deploy_init.py")
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class DeployInitTest(unittest.TestCase):
    def test_remote_profile_generates_unique_secrets_without_model_keys(self) -> None:
        ports = {"api": 8080, "agent": 8090, "web": 3000, "parser": 8070, "milvus": 19530, "milvus_health": 9091, "postgres": 5433, "redis": 6379}
        values = MODULE.deployment_values("remote", "localhost", ports)
        secrets = [values[name] for name in (
            "RAGLAB_AUTH_SECRET", "RAGLAB_PLATFORM_ADMIN_PASSWORD", "RAGLAB_TENANT_A_PASSWORD",
            "RAGLAB_TENANT_B_PASSWORD", "RAGLAB_CUSTOMER_PASSWORD", "RAGLAB_POSTGRES_PASSWORD",
            "RAGLAB_MINIO_ROOT_PASSWORD",
        )]
        self.assertEqual(len(secrets), len(set(secrets)))
        self.assertTrue(all(len(value) >= 32 for value in secrets))
        rendered = MODULE.render_env(values)
        self.assertNotIn("QWEN_API_KEY=", rendered)
        self.assertNotIn("DEEPSEEK_API_KEY=", rendered)
        self.assertEqual(values["RAGLAB_EMBEDDING_DIMENSIONS"], "1024")
        self.assertEqual(values["RAGLAB_RERANK_STRICT"], "true")

    def test_private_write_is_mode_600_and_refuses_accidental_rotation(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / ".env"
            MODULE.write_private(target, "VALUE=one\n", False)
            self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
            with self.assertRaises(FileExistsError):
                MODULE.write_private(target, "VALUE=two\n", False)


if __name__ == "__main__":
    unittest.main()
