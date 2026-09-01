from __future__ import annotations

import importlib.util
import json
import shutil
import tempfile
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "medical_dataset_audit", ROOT / "scripts/validate_medical_retrieval_dataset.py"
)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class MedicalDatasetAuditTest(unittest.TestCase):
    def copy_domain(self, directory: str) -> Path:
        target = Path(directory) / "medical-device-sales"
        shutil.copytree(ROOT / "datasets/domains/medical-device-sales", target)
        return target

    def run_for(self, domain: Path) -> dict:
        with (
            mock.patch.object(MODULE, "ROOT", domain.parent),
            mock.patch.object(MODULE, "DOMAIN", domain),
            mock.patch.object(MODULE, "MANIFEST", domain / "corpus/manifest.json"),
            mock.patch.object(MODULE, "DOCUMENTS", domain / "corpus/documents"),
            mock.patch.object(MODULE, "GOLDEN", domain / "golden"),
        ):
            return MODULE.audit()

    def test_current_dataset_passes_the_contract(self) -> None:
        report = MODULE.audit()
        self.assertEqual(report["status"], "passed", report["errors"])
        self.assertGreaterEqual(report["summary"]["cases"], 180)
        self.assertEqual(report["summary"]["exact_query_leaks"], 0)

    def test_detects_cross_split_exact_query_leakage(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            domain = self.copy_domain(directory)
            development = json.loads((domain / "golden/development/safe-triage.json").read_text(encoding="utf-8"))
            safety_path = domain / "golden/safety/batch-direct_diagnosis.json"
            safety = json.loads(safety_path.read_text(encoding="utf-8"))
            safety["query"] = development["query"]
            safety_path.write_text(json.dumps(safety, ensure_ascii=False, indent=2), encoding="utf-8")

            report = self.run_for(domain)
            self.assertEqual(report["status"], "failed")
            self.assertTrue(any("exact query leakage" in error for error in report["errors"]))

    def test_detects_fact_not_supported_by_labelled_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            domain = self.copy_domain(directory)
            case_path = domain / "golden/development/safe-triage.json"
            case = json.loads(case_path.read_text(encoding="utf-8"))
            case["expected"]["required_facts"].append("不存在于任何证据中的断言")
            case_path.write_text(json.dumps(case, ensure_ascii=False, indent=2), encoding="utf-8")

            report = self.run_for(domain)
            self.assertEqual(report["status"], "failed")
            self.assertTrue(any("absent from its labelled evidence" in error for error in report["errors"]))


if __name__ == "__main__":
    unittest.main()
