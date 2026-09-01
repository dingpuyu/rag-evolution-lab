from __future__ import annotations

import importlib.util
import json
import shutil
import tempfile
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("medical_bootstrap", ROOT / "scripts/medical_bootstrap.py")
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def inputs(sales_corpus: Path | None = None) -> tuple[list[dict], list[dict], dict[str, dict]]:
    sales_corpus = sales_corpus or MODULE.SALES_CORPUS
    manifest = json.loads((MODULE.CORPUS / "manifest.json").read_text(encoding="utf-8"))
    sales_manifest = json.loads((sales_corpus / "manifest.json").read_text(encoding="utf-8"))
    sales_lock = json.loads((sales_corpus / "sources.lock.json").read_text(encoding="utf-8"))["documents"]
    return manifest, sales_manifest, sales_lock


class MedicalBootstrapPlanTest(unittest.TestCase):
    def test_current_import_plan_is_complete_and_secret_free(self) -> None:
        plan = MODULE.build_import_plan(*inputs(), source_revision=1, skip_derived=False)
        self.assertEqual(plan["status"], "passed", plan["errors"])
        self.assertEqual(plan["summary"]["reviewed_sales_documents"], 39)
        self.assertEqual(plan["dataset_counts"]["public-medical-device-sales"], 39)
        self.assertEqual(plan["kind_counts"]["derived_format_fixture"], 4)
        rendered = json.dumps(plan, ensure_ascii=False)
        self.assertNotIn("QWEN_API_KEY", rendered)
        self.assertNotIn("DEEPSEEK_API_KEY", rendered)

    def test_rejects_sales_content_changed_after_source_review(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            copied = Path(directory) / "corpus"
            shutil.copytree(MODULE.SALES_CORPUS, copied)
            manifest, sales_manifest, sales_lock = inputs(copied)
            target = copied / sales_manifest[0]["path"]
            target.write_text(target.read_text(encoding="utf-8") + "\n未审核变更\n", encoding="utf-8")
            with mock.patch.object(MODULE, "SALES_CORPUS", copied):
                plan = MODULE.build_import_plan(
                    manifest,
                    sales_manifest,
                    sales_lock,
                    source_revision=1,
                    skip_derived=False,
                )
            self.assertEqual(plan["status"], "failed")
            self.assertTrue(any("content drifted after review" in error for error in plan["errors"]))


if __name__ == "__main__":
    unittest.main()
