from __future__ import annotations

import importlib.util
import json
import os
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "medical_retrieval_report", ROOT / "scripts/run_medical_retrieval_eval.py"
)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class MedicalRetrievalReportTest(unittest.TestCase):
    def test_parameter_fingerprint_is_stable_and_secret_free(self) -> None:
        environment = {
            "RAGLAB_RETRIEVAL_CANDIDATE_TOP_N": "20",
            "RAGLAB_CONTEXT_MAX_CHUNKS": "6",
            "RAGLAB_CONTEXT_TOKEN_BUDGET": "4000",
            "RAGLAB_PROVIDER_EVIDENCE_MIN_SCORE": "0.10",
            "RAGLAB_DOCUMENT_DIVERSIFICATION": "true",
            "QWEN_API_KEY": "must-not-enter-report",
        }
        with mock.patch.dict(os.environ, environment, clear=True):
            first = MODULE.experiment_profile(True, 350, 60)
            second = MODULE.experiment_profile(True, 350, 60)
        self.assertEqual(first, second)
        self.assertTrue(first["fingerprint"].startswith("sha256:"))
        self.assertNotIn("must-not-enter-report", json.dumps(first))

    def test_parameter_change_produces_new_fingerprint(self) -> None:
        with mock.patch.dict(os.environ, {}, clear=True):
            baseline = MODULE.experiment_profile(True, 350, 60)
        with mock.patch.dict(os.environ, {"RAGLAB_RETRIEVAL_CANDIDATE_TOP_N": "30"}, clear=True):
            candidate = MODULE.experiment_profile(True, 350, 60)
        self.assertNotEqual(baseline["fingerprint"], candidate["fingerprint"])


if __name__ == "__main__":
    unittest.main()
