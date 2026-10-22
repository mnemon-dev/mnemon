"""Copy a live SQLite store using online backup, without querying Mnemon tables."""
import json
from pathlib import Path
import sqlite3
import sys
from contextlib import closing

request = json.load(sys.stdin)
source = Path(request["source"]).resolve(strict=True)
target = Path(request["target"]).resolve()
target.parent.mkdir(parents=True, exist_ok=True)
with closing(sqlite3.connect(source.as_uri() + "?mode=ro", uri=True, timeout=3)) as reader:
    with closing(sqlite3.connect(str(target))) as writer:
        with writer:
            reader.backup(writer, pages=256, sleep=0.03)
print(json.dumps({"ok": True}))
