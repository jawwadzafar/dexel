#!/usr/bin/env python3
"""Ask a vision model a question about a screenshot.

Exists because the fleet's own models are text-only: M2's and M3's exit
criteria are visual ("the progress bar visibly moves when you type") and no
agent could ever verify them, so those checks kept getting logged as
unproven. This calls a vision-capable model directly over HTTP rather than
relying on the agent runtime to feed an image into a subagent's context.

Usage:
    python3 scripts/visual-check.py <image-path> "<question>"

Exit codes:
    0  got an answer (printed to stdout)
    2  bad usage / image missing
    3  no API key or model available
    4  the vision call failed

The API key is read from ~/.config/opencode/opencode.jsonc (or $TF_API_KEY)
so no secret is ever committed to this repo.
"""

import base64
import json
import os
import re
import subprocess
import sys
from pathlib import Path

CONFIG = Path.home() / ".config" / "opencode" / "opencode.jsonc"
ENDPOINT = "https://tf-stage-api.iamsaif.ai/v1/chat/completions"
# Verified vision-capable 2026-08-20. First entry that answers wins; the
# fallback matters because these are staging deployments that go down (a
# text-only DeepSeek outage already stalled the fleet once).
MODELS = ["google/gemma-4-31B-it", "Qwen/Qwen3-Omni-30B-A3B-Instruct"]


def api_key() -> str | None:
    if os.environ.get("TF_API_KEY"):
        return os.environ["TF_API_KEY"]
    if not CONFIG.exists():
        return None
    # jsonc -> json: strip //-comments that are not inside a string
    raw = re.sub(r'^\s*//.*$', '', CONFIG.read_text(), flags=re.M)
    try:
        cfg = json.loads(raw)
    except json.JSONDecodeError:
        return None
    for prov in cfg.get("provider", {}).values():
        key = prov.get("options", {}).get("apiKey")
        if key:
            return key
    return None


def ask(model: str, key: str, b64: str, question: str) -> tuple[bool, str]:
    body = json.dumps({
        "model": model,
        "max_tokens": 300,
        "temperature": 0,
        "messages": [{"role": "user", "content": [
            {"type": "text", "text": question},
            {"type": "image_url",
             "image_url": {"url": f"data:image/png;base64,{b64}"}},
        ]}],
    })
    try:
        proc = subprocess.run(
            ["curl", "-s", "--max-time", "90", "-X", "POST", ENDPOINT,
             "-H", f"Authorization: Bearer {key}",
             "-H", "Content-Type: application/json", "-d", body],
            capture_output=True, text=True, timeout=100,
        )
    except subprocess.TimeoutExpired:
        return False, "request timed out"
    if not proc.stdout.strip():
        # An empty body is what a dropped/hung upstream looks like.
        return False, "empty response (model may be down)"
    try:
        data = json.loads(proc.stdout)
    except json.JSONDecodeError:
        return False, f"unparseable response: {proc.stdout[:200]}"
    if "error" in data:
        return False, str(data["error"].get("message") or data["error"])[:200]
    content = (data.get("choices") or [{}])[0].get("message", {}).get("content")
    if not content:
        return False, "model returned empty content"
    return True, content.strip()


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    path, question = Path(sys.argv[1]), sys.argv[2]
    if not path.is_file():
        print(f"ERROR: no such image: {path}", file=sys.stderr)
        return 2
    key = api_key()
    if not key:
        print(f"ERROR: no API key in {CONFIG} or $TF_API_KEY", file=sys.stderr)
        return 3

    b64 = base64.b64encode(path.read_bytes()).decode()
    problems = []
    for model in MODELS:
        ok, out = ask(model, key, b64, question)
        if ok:
            print(f"[vision model: {model}]")
            print(out)
            return 0
        problems.append(f"  {model}: {out}")
    print("ERROR: every vision model failed:\n" + "\n".join(problems),
          file=sys.stderr)
    return 4


if __name__ == "__main__":
    sys.exit(main())
