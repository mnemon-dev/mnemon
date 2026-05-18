#!/usr/bin/env python3
"""Install or remove Mnemon memory loop hooks from Claude Code settings.json."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


EVENTS = ("SessionStart", "UserPromptSubmit", "Stop", "PreCompact")


def load_json(path: Path) -> dict[str, Any]:
    if not path.exists() or path.stat().st_size == 0:
        return {}
    return json.loads(strip_json5(path.read_text()))


def strip_json5(text: str) -> str:
    out: list[str] = []
    in_string = False
    escaped = False
    i = 0
    while i < len(text):
        ch = text[i]
        if escaped:
            out.append(ch)
            escaped = False
            i += 1
            continue
        if in_string:
            if ch == "\\":
                escaped = True
            elif ch == '"':
                in_string = False
            out.append(ch)
            i += 1
            continue
        if ch == '"':
            in_string = True
            out.append(ch)
            i += 1
            continue
        if ch == "/" and i + 1 < len(text) and text[i + 1] == "/":
            while i < len(text) and text[i] != "\n":
                i += 1
            continue
        if ch == ",":
            j = i + 1
            while j < len(text) and text[j] in " \t\r\n":
                j += 1
            if j < len(text) and text[j] in "]}":
                i += 1
                continue
        out.append(ch)
        i += 1
    return "".join(out)


def write_json(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + "\n")


def contains_mnemon(value: Any) -> bool:
    if isinstance(value, str):
        return "mnemon-memory-loop" in value
    if isinstance(value, dict):
        return any(contains_mnemon(item) for item in value.values())
    if isinstance(value, list):
        return any(contains_mnemon(item) for item in value)
    return False


def remove_hooks(data: dict[str, Any]) -> None:
    hooks = data.get("hooks")
    if not isinstance(hooks, dict):
        return
    for event in EVENTS:
        entries = hooks.get(event)
        if not isinstance(entries, list):
            continue
        kept = [entry for entry in entries if not contains_mnemon(entry)]
        if kept:
            hooks[event] = kept
        else:
            hooks.pop(event, None)
    if not hooks:
        data.pop("hooks", None)


def hook_entry(command: Path) -> dict[str, Any]:
    return {
        "hooks": [
            {
                "type": "command",
                "command": str(command),
            }
        ]
    }


def add_hook(data: dict[str, Any], event: str, command: Path) -> None:
    hooks = data.get("hooks")
    if not isinstance(hooks, dict):
        hooks = {}
        data["hooks"] = hooks
    entries = hooks.setdefault(event, [])
    if not isinstance(entries, list):
        entries = []
        hooks[event] = entries
    entries.append(hook_entry(command))


def install(args: argparse.Namespace) -> None:
    config_dir = Path(args.config_dir)
    settings_path = config_dir / "settings.json"
    hooks_dir = config_dir / "hooks" / "mnemon-memory-loop"

    data = load_json(settings_path)
    remove_hooks(data)

    add_hook(data, "SessionStart", hooks_dir / "prime.sh")
    if args.remind == "1":
        add_hook(data, "UserPromptSubmit", hooks_dir / "remind.sh")
    if args.nudge == "1":
        add_hook(data, "Stop", hooks_dir / "nudge.sh")
    if args.compact == "1":
        add_hook(data, "PreCompact", hooks_dir / "compact.sh")

    write_json(settings_path, data)


def uninstall(args: argparse.Namespace) -> None:
    config_dir = Path(args.config_dir)
    settings_path = config_dir / "settings.json"
    data = load_json(settings_path)
    remove_hooks(data)
    if data:
        write_json(settings_path, data)
    elif settings_path.exists():
        settings_path.unlink()


def main() -> None:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    install_parser = subparsers.add_parser("install")
    install_parser.add_argument("--config-dir", required=True)
    install_parser.add_argument("--remind", choices=("0", "1"), required=True)
    install_parser.add_argument("--nudge", choices=("0", "1"), required=True)
    install_parser.add_argument("--compact", choices=("0", "1"), required=True)
    install_parser.set_defaults(func=install)

    uninstall_parser = subparsers.add_parser("uninstall")
    uninstall_parser.add_argument("--config-dir", required=True)
    uninstall_parser.set_defaults(func=uninstall)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
