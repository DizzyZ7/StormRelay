#!/usr/bin/env python3
"""Combine Go benchmark and k6 output into StormRelay benchmark-result/v1."""

from __future__ import annotations

import argparse
import json
import os
import platform
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", required=True, type=Path)
    parser.add_argument("--k6-summary", required=True, type=Path)
    parser.add_argument("--go-benchmark", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--commit-sha")
    parser.add_argument("--base-url", default="http://localhost:8080")
    return parser.parse_args()


def read_json(path: Path) -> dict[str, Any]:
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return data


def command_output(command: list[str], fallback: str) -> str:
    try:
        result = subprocess.run(
            command,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=15,
        )
    except (OSError, subprocess.SubprocessError):
        return fallback
    value = result.stdout.strip()
    return value or fallback


def cpu_model() -> str:
    try:
        for line in Path("/proc/cpuinfo").read_text(encoding="utf-8").splitlines():
            if line.lower().startswith("model name") and ":" in line:
                return line.split(":", 1)[1].strip()
    except OSError:
        pass
    return platform.processor() or platform.machine() or "unknown"


def memory_bytes() -> int:
    try:
        for line in Path("/proc/meminfo").read_text(encoding="utf-8").splitlines():
            if line.startswith("MemTotal:"):
                return int(line.split()[1]) * 1024
    except (OSError, ValueError, IndexError):
        pass
    page_size = getattr(os, "sysconf", lambda _: 0)("SC_PAGE_SIZE")
    pages = getattr(os, "sysconf", lambda _: 0)("SC_PHYS_PAGES")
    value = int(page_size) * int(pages)
    return value if value > 0 else 1


def git_commit(explicit: str | None) -> str:
    if explicit:
        value = explicit.strip().lower()
    else:
        value = command_output(["git", "rev-parse", "HEAD"], "unknown").lower()
    if value == "unknown" or not (7 <= len(value) <= 40):
        raise ValueError("a 7-40 character hexadecimal commit SHA is required")
    try:
        int(value, 16)
    except ValueError as exc:
        raise ValueError("commit SHA must be hexadecimal") from exc
    return value


def git_dirty() -> bool:
    output = command_output(["git", "status", "--porcelain"], "unknown")
    return output not in ("", "unknown")


def metric_values(summary: dict[str, Any], name: str) -> dict[str, Any]:
    metrics = summary.get("metrics")
    if not isinstance(metrics, dict) or name not in metrics:
        raise ValueError(f"k6 summary is missing metric {name!r}")
    metric = metrics[name]
    if not isinstance(metric, dict) or not isinstance(metric.get("values"), dict):
        raise ValueError(f"k6 metric {name!r} has no values")
    return metric["values"]


def latency_metric(summary: dict[str, Any], name: str) -> dict[str, Any]:
    values = metric_values(summary, name)
    return {
        "unit": "ms",
        "samples": int(values.get("count", 0)),
        "p50": float(values.get("med", 0)),
        "p95": float(values.get("p(95)", 0)),
        "p99": float(values.get("p(99)", 0)),
    }


def counter(summary: dict[str, Any], name: str) -> int:
    return int(metric_values(summary, name).get("count", 0))


def parse_go_benchmarks(path: Path) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line.startswith("Benchmark"):
            continue
        fields = line.split()
        if len(fields) < 4:
            continue
        try:
            iterations = int(fields[1])
        except ValueError:
            continue
        result: dict[str, Any] = {"name": fields[0], "iterations": iterations}
        index = 2
        while index + 1 < len(fields):
            try:
                value = float(fields[index])
            except ValueError:
                index += 1
                continue
            unit = fields[index + 1]
            if unit == "ns/op":
                result["nanoseconds_per_operation"] = value
            elif unit == "B/op":
                result["bytes_per_operation"] = value
            elif unit == "allocs/op":
                result["allocations_per_operation"] = value
            elif unit == "MB/s":
                result["megabytes_per_second"] = value
            index += 2
        if "nanoseconds_per_operation" in result:
            results.append(result)
    if not results:
        raise ValueError(f"no Go benchmark records found in {path}")
    return results


def main() -> int:
    args = parse_args()
    try:
        profile = read_json(args.profile)
        summary = read_json(args.k6_summary)
        dependencies = profile.get("dependencies", {})
        timing = profile.get("timing", {})
        error_values = metric_values(summary, "stormrelay_errors")
        report = {
            "schema_version": "stormrelay.benchmark-result/v1",
            "generated_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
            "commit_sha": git_commit(args.commit_sha),
            "profile": profile,
            "environment": {
                "hardware": {
                    "cpu_model": cpu_model(),
                    "logical_cpus": os.cpu_count() or 1,
                    "memory_bytes": memory_bytes(),
                },
                "os": {
                    "name": platform.system() or "unknown",
                    "version": platform.release() or "unknown",
                    "architecture": platform.machine() or "unknown",
                },
                "runtimes": {
                    "go": command_output(["go", "version"], "unavailable"),
                    "docker": command_output(
                        ["docker", "version", "--format", "{{.Server.Version}}"],
                        "unavailable",
                    ),
                    "docker_compose": command_output(
                        ["docker", "compose", "version", "--short"], "unavailable"
                    ),
                    "k6": str(dependencies.get("k6_image", "unavailable")),
                    "postgres": str(dependencies.get("postgres_image", "unavailable")),
                    "nats": str(dependencies.get("nats_image", "unavailable")),
                },
            },
            "configuration": {
                "base_url": args.base_url,
                "database_target": "postgresql://localhost:5432/stormrelay",
                "nats_target": "nats://localhost:4222",
            },
            "phase": {
                "cold_start": False,
                "cache_state": "warmed",
                "warmup_seconds": float(timing.get("warmup_seconds", 0)),
                "measurement_seconds": float(timing.get("duration_seconds", 0)),
            },
            "metrics": {
                "http_acceptance_ms": latency_metric(
                    summary, "stormrelay_http_acceptance_ms"
                ),
                "event_to_incident_ms": latency_metric(
                    summary, "stormrelay_event_to_incident_ms"
                ),
                "error_rate": float(error_values.get("rate", 0)),
                "accepted_events": counter(summary, "stormrelay_accepted_events"),
                "durable_events": counter(summary, "stormrelay_durable_events"),
                "duplicate_requests": counter(
                    summary, "stormrelay_duplicate_requests"
                ),
            },
            "go_benchmarks": parse_go_benchmarks(args.go_benchmark),
            "provenance": {
                "generated_by": "tests/load/capture_result.py",
                "profile_path": str(args.profile),
                "k6_summary_path": str(args.k6_summary),
                "go_benchmark_path": str(args.go_benchmark),
                "git_dirty": git_dirty(),
            },
        }
        args.output.parent.mkdir(parents=True, exist_ok=True)
        temporary = args.output.with_suffix(args.output.suffix + ".tmp")
        temporary.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        temporary.replace(args.output)
        print(args.output)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"capture benchmark result: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
