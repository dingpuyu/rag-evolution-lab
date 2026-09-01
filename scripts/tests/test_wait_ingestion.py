import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("wait_ingestion", ROOT / "scripts/wait_ingestion.py")
wait_ingestion = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(wait_ingestion)


class WaitIngestionTest(unittest.TestCase):
    def test_job_report_selects_exact_current_release_jobs(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "jobs.json"
            path.write_text(json.dumps({
                "schema_version": 1,
                "documents": [
                    {"job_id": "job_current_a", "status": "queued"},
                    {"job_id": "job_current_b", "status": "completed"},
                    {"job_id": "", "status": "completed"},
                ],
            }), encoding="utf-8")

            self.assertEqual(
                wait_ingestion.expected_job_ids(path),
                {"job_current_a", "job_current_b"},
            )

    def test_empty_job_report_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "jobs.json"
            path.write_text('{"documents": []}', encoding="utf-8")

            with self.assertRaisesRegex(RuntimeError, "contains no job IDs"):
                wait_ingestion.expected_job_ids(path)


if __name__ == "__main__":
    unittest.main()
