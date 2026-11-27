"""Offline Git control validation; never executes instruction strings."""
import json
import re
from pathlib import Path
from jsonschema import Draft202012Validator, FormatChecker

ROOT = Path(__file__).resolve().parents[2]
NAMES = ("config", "tasks", "progress", "result")
MAX_BYTES = 128 * 1024

def read_json(path):
    raw = path.read_bytes()
    if len(raw) > MAX_BYTES:
        raise ValueError("control file too large")
    def unique(pairs):
        out = {}
        for key, value in pairs:
            if key in out:
                raise ValueError("duplicate JSON key")
            out[key] = value
        return out
    return json.loads(raw.decode("utf-8"), object_pairs_hook=unique,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError("invalid JSON constant")))

def validate(name, data):
    if name not in NAMES:
        raise ValueError("unknown control document")
    schema = read_json(ROOT / "schemas" / (name + ".schema.json"))
    Draft202012Validator.check_schema(schema)
    Draft202012Validator(schema, format_checker=FormatChecker()).validate(data)

def valid_branch(branch):
    return (branch != "@" and not branch.startswith("-")
            and all(part and not part.startswith(".") and not part.endswith(".lock")
                    for part in branch.split("/"))
            and not branch.endswith(".") and ".." not in branch and "@{" not in branch)

def validate_bundle(bundle, previous_revision=None):
    if set(bundle) != set(NAMES):
        raise ValueError("missing or extra control documents")
    for name in NAMES:
        validate(name, bundle[name])
    config, tasks, progress, result = (bundle[name] for name in NAMES)
    if any(doc["task_id"] != config["task_id"] for doc in bundle.values()):
        raise ValueError("task mismatch")
    if config["control_branch"] != "ai-control/" + config["task_id"]:
        raise ValueError("control branch mismatch")
    if not valid_branch(config["code_branch"]) or config["code_branch"].startswith("ai-control/") or config["code_branch"] == "ai-control":
        raise ValueError("invalid code branch")
    if any(part in (".", "..") for part in config["repository"].split("/")):
        raise ValueError("invalid repository")
    ids = [stage["id"] for stage in config["stages"]]
    task_ids = [stage["id"] for stage in tasks["stages"]]
    if len(set(ids)) != len(ids) or len(set(task_ids)) != len(task_ids) or set(ids) != set(task_ids):
        raise ValueError("stage mismatch or duplicate")
    config_stages = {stage["id"]: stage for stage in config["stages"]}
    graph = {stage["id"]: stage["dependencies"] for stage in tasks["stages"]}
    for stage in tasks["stages"]:
        if set(stage["acceptance"]) != set(config_stages[stage["id"]]["acceptance"]):
            raise ValueError("acceptance mismatch")
        if not set(stage["dependencies"]).issubset(graph):
            raise ValueError("unknown dependency")
    pending = set(graph)
    while pending:
        ready = {key for key in pending if not (set(graph[key]) & pending)}
        if not ready:
            raise ValueError("dependency cycle")
        pending -= ready
    for field in ("revision", "stage_id", "code_sha", "dispatch_id"):
        if progress[field] != result[field]:
            raise ValueError("progress/result mismatch: " + field)
    if previous_revision is not None and progress["revision"] <= previous_revision:
        raise ValueError("stale revision")
    if progress["stage_id"] not in graph or not set(progress["completed_stage_ids"]).issubset(graph):
        raise ValueError("unknown stage")
    for evidence in result["evidence"]:
        prefix = "https://github.com/" + config["repository"] + "/"
        if not evidence["url"].startswith(prefix):
            raise ValueError("evidence repository mismatch")
        suffix = evidence["url"][len(prefix):]
        pattern = {"commit": r"commit/[0-9a-f]{40}", "pull_request": r"pull/[1-9][0-9]*",
                   "workflow_run": r"actions/runs/[1-9][0-9]*",
                   "artifact": r"actions/runs/[1-9][0-9]*/artifacts/[1-9][0-9]*"}[evidence["kind"]]
        if not re.fullmatch(pattern, suffix):
            raise ValueError("evidence kind mismatch")
        if evidence["kind"] == "commit" and suffix != "commit/" + progress["code_sha"]:
            raise ValueError("evidence SHA mismatch")
    # Actual Git reachability, CI success, and dispatch authorization are future service checks.
    return True
