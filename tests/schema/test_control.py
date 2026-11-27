import copy
import tempfile
import unittest
from pathlib import Path
from jsonschema import ValidationError
from validate import ROOT, NAMES, read_json, validate_bundle

class ControlTests(unittest.TestCase):
    def setUp(self):
        self.bundle = {name: read_json(ROOT / "schemas/examples" / (name + ".json")) for name in NAMES}

    def test_valid(self):
        self.assertTrue(validate_bundle(self.bundle, previous_revision=11))

    def reject(self, name, key, value):
        bundle = copy.deepcopy(self.bundle)
        bundle[name][key] = value
        with self.assertRaises((ValueError, ValidationError)):
            validate_bundle(bundle)

    def test_invalid_fields(self):
        for name, key, value in [
            ("config", "secret", "fake"), ("config", "code_branch", "feat/../x"),
            ("config", "code_branch", "ai-control/DEMO-001"),
            ("config", "control_branch", "ai-control/OTHER"),
            ("config", "poll_seconds", 0), ("progress", "status", "SUCCESS"),
            ("progress", "code_sha", "bad"), ("progress", "task_id", "OTHER"),
            ("progress", "updated_at", "2026-02-30T00:00:00Z"),
            ("progress", "revision", 11), ("result", "stage_id", "OTHER"),
            ("progress", "completed_stage_ids", ["S9"]),
            ("result", "evidence", [{"kind": "commit", "url": "https://evil.example/x"}]),
            ("result", "evidence", [{"kind": "workflow_run", "url": "https://github.com/other/demo/actions/runs/123"}]),
            ("result", "evidence", [{"kind": "commit", "url": "https://github.com/owner/demo/actions/runs/123"}]),
        ]:
            with self.subTest(name=name, key=key, value=value):
                self.reject(name, key, value)

    def test_stale(self):
        with self.assertRaises(ValueError):
            validate_bundle(self.bundle, previous_revision=12)

    def test_dependency_cycle(self):
        self.bundle["tasks"]["stages"][0]["dependencies"] = ["S1"]
        with self.assertRaises(ValueError):
            validate_bundle(self.bundle)

    def test_duplicate_stage(self):
        self.bundle["config"]["stages"] *= 2
        with self.assertRaises(ValueError):
            validate_bundle(self.bundle)

    def test_parser_rejects_duplicates_and_oversize(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "data.json"
            for raw in (b'{"x":1,"x":2}', b'{"x":NaN}', b' ' * (128*1024+1)):
                path.write_bytes(raw)
                with self.assertRaises(ValueError):
                    read_json(path)

if __name__ == "__main__":
    unittest.main()
