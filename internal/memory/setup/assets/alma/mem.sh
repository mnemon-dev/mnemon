#!/bin/bash
# mem — Alma ↔ mnemon bridge shim
#
# Installed by `mnemon setup --target alma`. mnemon is the single source of
# truth; this shim is the only component that knows how to reach it, and the
# skill, the hook and the model all go through here.
#
# It owns exactly one region of ~/.config/alma/MEMORY.md — the block between
# `<!-- mnemon:begin -->` and `<!-- mnemon:end -->`. Every byte outside those
# markers belongs to Alma and the user and is never rewritten.
#
# Usage:
#   mem find "<query>" [--limit N] [--cat cat] [--chars N]
#   mem save "<content>" --cat preference|decision|fact|insight|context
#                        [--imp 1-5] [--tags a,b] [--entities a,b] [--source user|agent]
#   mem show <id>              # accepts an 8+ character id prefix
#   mem related <id> [--edge causal|entity|semantic|supersedes|temporal]
#   mem recent [N]
#   mem status
#   mem sync                   # refresh the mnemon block in MEMORY.md
#   mem forget <id>            # soft delete
#
# Env: MNEMON_BRIDGE_STORE (default: default), ALMA_CONFIG_DIR

set -uo pipefail

STORE="${MNEMON_BRIDGE_STORE:-default}"
ALMA_DIR="${ALMA_CONFIG_DIR:-$HOME/.config/alma}"
MEMORY_MD="$ALMA_DIR/MEMORY.md"
BRIDGE_DIR="$ALMA_DIR/mnemon-bridge"
BEGIN_MARK='<!-- mnemon:begin -->'
END_MARK='<!-- mnemon:end -->'

die() { echo "mem: $*" >&2; exit 1; }
have_mnemon() { command -v mnemon >/dev/null 2>&1 || die "mnemon not found in PATH"; }

# Resolve the database path from mnemon itself, so MNEMON_DATA_DIR and store
# layout changes are mnemon's business, not ours.
DB=""
resolve_db() {
  [ -n "$DB" ] && return 0
  local st
  st=$(mnemon status --store "$STORE" --readonly 2>/dev/null) || return 1
  DB=$(printf '%s\n' "$st" | sed -n 's/.*"db_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$DB" ] && [ -f "$DB" ]
}

# Read-only sqlite handle. This database runs in WAL mode, where both
# `sqlite3 -readonly` and `?mode=ro` fail with "unable to open database file
# (14)". `?immutable=1` opens, and stays correct because mnemon commits to the
# main file rather than leaving the reader pinned to a stale snapshot
# (verified: a row added after opening is visible on the next invocation).
rox() { resolve_db || return 1; sqlite3 "file:$DB?immutable=1" "$1" 2>/dev/null; }

# Resolve an id prefix to a full UUID. Passes full UUIDs through unchanged.
resolve_id() {
  local want="$1" got
  [ -n "$want" ] || return 1
  case "$want" in
    *-*) printf '%s' "$want"; return 0 ;;
  esac
  got=$(rox "select id from insights where id like '${want}%' limit 1;")
  [ -n "$got" ] || return 1
  printf '%s' "$got"
}

# ---- subcommands ----------------------------------------------------------
cmd_find() {
  local q="" limit=5 cat="" chars=240 out
  while [ $# -gt 0 ]; do
    case "$1" in
      --limit|-n) limit="$2"; shift 2 ;;
      --cat|-c)   cat="$2";   shift 2 ;;
      --chars)    chars="$2"; shift 2 ;;
      -*)         die "unknown flag $1" ;;
      *)          q="${q:+$q }$1"; shift ;;
    esac
  done
  [ -n "$q" ] || die "usage: mem find <query>"
  case "$limit" in ''|*[!0-9]*) die "invalid --limit $limit" ;; esac
  case "$chars" in ''|*[!0-9]*) die "invalid --chars $chars" ;; esac
  case "$cat" in
    ""|preference|decision|fact|insight|context|general) ;;
    *) die "invalid --cat $cat (preference|decision|fact|insight|context|general)" ;;
  esac

  local args=(recall "$q" --store "$STORE" --readonly --limit "$limit" --brief --excerpt-chars "$chars")
  [ -n "$cat" ] && args+=(--cat "$cat")

  out=$(mnemon "${args[@]}" 2>&1) || { echo "mem: recall failed: $out" >&2; exit 1; }

  # mnemon 0.2.13 parses --cat but does not filter on it (asking for
  # `preference` returns `decision` rows in the same score band), so filter here.
  python3 - "$out" "$cat" <<'PY'
import sys, json
raw, want = sys.argv[1], sys.argv[2]
try:
    d = json.loads(raw)
except Exception:
    print(raw); sys.exit(0)
res = d.get("results") or []
if want:
    res = [r for r in res if (r.get("category") or "") == want]
if not res:
    print("(no matches)"); sys.exit(0)
for i, r in enumerate(res, 1):
    ex = " ".join((r.get("excerpt") or "").split())
    cat = r.get("category") or "?"
    score = r.get("score")
    sid = (r.get("id") or "")[:8]
    s = f"{score:.2f}" if isinstance(score, (int, float)) else "?"
    print(f"{i}. [{cat} {s}] {ex}")
    print(f"   id {sid}")
PY
}

cmd_save() {
  local content="" cat="" imp=3 tags="" entities="" source="agent" out
  while [ $# -gt 0 ]; do
    case "$1" in
      --cat|-c)      cat="$2";      shift 2 ;;
      --imp|-i)      imp="$2";      shift 2 ;;
      --tags|-t)     tags="$2";     shift 2 ;;
      --entities|-e) entities="$2"; shift 2 ;;
      --source|-s)   source="$2";   shift 2 ;;
      -*)            die "unknown flag $1" ;;
      *)             content="${content:+$content }$1"; shift ;;
    esac
  done
  [ -n "$content" ] || die "usage: mem save <content> --cat <cat> [--imp N]"
  case "$cat" in
    preference|decision|fact|insight|context) ;;
    "")  die "missing --cat (preference|decision|fact|insight|context)" ;;
    *)   die "invalid --cat $cat (general is the unclassified default and is rejected)" ;;
  esac
  case "$imp" in 1|2|3|4|5) ;; *) die "invalid --imp $imp (1-5)" ;; esac
  case "$source" in user|agent) ;; *) die "invalid --source $source (user|agent)" ;; esac

  local args=(remember "$content" --store "$STORE" --cat "$cat" --imp "$imp" --source "$source")
  [ -n "$tags" ] && args+=(--tags "$tags")
  [ -n "$entities" ] && args+=(--entities "$entities")

  out=$(mnemon "${args[@]}" 2>&1) || { echo "mem: save failed: $out" >&2; exit 1; }
  python3 - "$out" <<'PY'
import sys, json
try: d = json.loads(sys.argv[1])
except Exception: print(sys.argv[1]); sys.exit(0)
i = d.get("insight") or d.get("result") or d
sid = (i.get("id") or "")[:8] if isinstance(i, dict) else ""
act = d.get("action") or d.get("status") or "stored"
print(f"saved {sid} ({act})")
for k in ("duplicate_of", "conflicts_with", "merged_into"):
    v = d.get(k) or (i.get(k) if isinstance(i, dict) else None)
    if v: print(f"  {k}: {v}")
PY
}

cmd_show() {
  [ $# -ge 1 ] || die "usage: mem show <id>"
  local id
  id=$(resolve_id "$1") || die "no insight matching id prefix '$1'"
  mnemon show "$id" --store "$STORE" --readonly 2>&1 | python3 -c '
import sys, json
try: d = json.load(sys.stdin)
except Exception: print(sys.stdin.read()); sys.exit(0)
i = d.get("insight") or d
print(i.get("content",""))
for k in ("category","importance","tags","entities","created_at"):
    if i.get(k) not in (None,"",[]): print(f"{k}: {i.get(k)}")'
}

cmd_related() {
  [ $# -ge 1 ] || die "usage: mem related <id> [--edge T]"
  local id
  id=$(resolve_id "$1") || die "no insight matching id prefix '$1'"
  shift || true
  mnemon related "$id" --store "$STORE" --readonly "$@" 2>&1 | python3 -c '
import sys, json
try: d = json.load(sys.stdin)
except Exception: print(sys.stdin.read()); sys.exit(0)
# mnemon 0.2.13 returns a bare array here, not {"related": [...]}
rs = (d.get("related") or d.get("results") or []) if isinstance(d, dict) else (d or [])
if not rs: print("(nothing related)"); sys.exit(0)
for r in rs:
    ex = " ".join((r.get("content") or r.get("excerpt") or "").split())[:160]
    et = r.get("edge_type") or r.get("category") or "?"
    print(f"- [{et}] {ex}")'
}

cmd_recent() {
  local n="${1:-10}"
  case "$n" in ''|*[!0-9]*) die "usage: mem recent [N]" ;; esac
  rox "select id, category, importance, substr(replace(replace(content,char(10),' '),char(13),' '),1,200)
         from insights where deleted_at is null
        order by created_at desc limit $n;" || die "cannot read $STORE"
}

cmd_status() {
  local st
  st=$(mnemon status --store "$STORE" --readonly 2>&1) || die "mnemon status failed: $st"
  printf '%s' "$st" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print('store=%s insights=%s edges=%s deleted=%s size=%sKB' % (
    sys.argv[1], d.get('total_insights', 0), d.get('edge_count', 0),
    d.get('deleted_insights', 0), d.get('db_size_bytes', 0) // 1024))
bc = d.get('by_category') or {}
if bc: print('categories: ' + ', '.join('%s=%s' % (k, v) for k, v in sorted(bc.items(), key=lambda x: -x[1])))
te = d.get('top_entities') or []
if te: print('entities: ' + ', '.join('%s(%s)' % (e['entity'], e['count']) for e in te[:8]))
" "$STORE"
}

cmd_sync() {
  command -v sqlite3 >/dev/null 2>&1 || die "sqlite3 not found"
  resolve_db || die "cannot locate the $STORE database"

  local block out rc=0
  block=$(mktemp) || die "mktemp failed"
  out=$(mktemp) || { rm -f "$block"; die "mktemp failed"; }

  {
    echo "$BEGIN_MARK"
    echo "## Mnemon hot index"
    echo
    echo "> Auto-generated from the mnemon \`$STORE\` store. Do not edit this block by hand."
    echo
    local rows
    rows=$(rox "select category || char(31) || importance || char(31) ||
                      substr(replace(replace(content,char(10),' '),char(13),' '),1,120)
                 from insights
                where deleted_at is null and importance>=4
                order by importance desc, created_at desc, id asc limit 16;") || {
      rm -f "$block" "$out"; die "sqlite query failed; MEMORY.md left untouched"
    }
    if [ -n "$rows" ]; then
      printf '%s\n' "$rows" | awk -F'\037' '{ printf "- [%s i%s] %s\n", $1, $2, $3 }'
    else
      echo "- (no high-importance memories yet)"
    fi
    echo "$END_MARK"
  } > "$block"

  if [ ! -e "$MEMORY_MD" ]; then
    mkdir -p "$(dirname "$MEMORY_MD")" || { rm -f "$block" "$out"; die "cannot create $(dirname "$MEMORY_MD")"; }
    cp "$block" "$out"
  else
    local nb ne
    nb=$(grep -c -F -- "$BEGIN_MARK" "$MEMORY_MD" 2>/dev/null || true)
    ne=$(grep -c -F -- "$END_MARK"   "$MEMORY_MD" 2>/dev/null || true)
    if [ "${nb:-0}" -eq 0 ] && [ "${ne:-0}" -eq 0 ]; then
      # No block yet. If the file is still an older whole-file mnemon dump,
      # replace it — appending would duplicate every line.
      if head -n 1 "$MEMORY_MD" 2>/dev/null | grep -q '^# MEMORY\.md — hot index$'; then
        mkdir -p "$BRIDGE_DIR" 2>/dev/null || true
        cp "$MEMORY_MD" "$BRIDGE_DIR/MEMORY.md.pre-migration.$$" 2>/dev/null || true
        cp "$block" "$out"
      else
        cat "$MEMORY_MD" > "$out"
        [ -s "$MEMORY_MD" ] && printf '\n' >> "$out"
        cat "$block" >> "$out"
      fi
    elif [ "${nb:-2}" -eq 1 ] && [ "${ne:-2}" -eq 1 ]; then
      awk -v block="$block" -v bm="$BEGIN_MARK" -v em="$END_MARK" '
        BEGIN { done=0; inb=0 }
        {
          if (!done && index($0, bm) > 0) {
            while ((getline l < block) > 0) print l
            close(block); done=1; inb=1; next
          }
          if (inb) { if (index($0, em) > 0) inb=0; next }
          print
        }
        END { if (!done) exit 2 }
      ' "$MEMORY_MD" > "$out" || rc=$?
      if [ "$rc" -ne 0 ]; then
        rm -f "$block" "$out"
        die "malformed markers in $MEMORY_MD; left untouched"
      fi
    else
      rm -f "$block" "$out"
      die "malformed markers in $MEMORY_MD (begin=$nb end=$ne); left untouched"
    fi
  fi

  if [ -e "$MEMORY_MD" ] && cmp -s "$out" "$MEMORY_MD"; then
    rm -f "$block" "$out"
    echo "unchanged $MEMORY_MD ($(wc -c < "$MEMORY_MD" | tr -d ' ') bytes)"
    return 0
  fi

  local mode
  mode=$(stat -f '%Lp' "$MEMORY_MD" 2>/dev/null || stat -c '%a' "$MEMORY_MD" 2>/dev/null || echo 600)
  chmod "$mode" "$out" 2>/dev/null || true
  mv "$out" "$MEMORY_MD" || { rm -f "$block" "$out"; die "cannot replace $MEMORY_MD"; }
  rm -f "$block"
  echo "wrote $MEMORY_MD ($(wc -l < "$MEMORY_MD" | tr -d ' ') lines, $(wc -c < "$MEMORY_MD" | tr -d ' ') bytes)"
}

cmd_forget() {
  [ $# -ge 1 ] || die "usage: mem forget <id>"
  local id
  id=$(resolve_id "$1") || die "no insight matching id prefix '$1'"
  mnemon forget "$id" --store "$STORE" 2>&1
}

# ---- dispatch -------------------------------------------------------------
have_mnemon
case "${1:-}" in
  find|recall)   shift; cmd_find "$@" ;;
  save|remember) shift; cmd_save "$@" ;;
  show)          shift; cmd_show "$@" ;;
  related)       shift; cmd_related "$@" ;;
  recent)        shift; cmd_recent "$@" ;;
  status)        shift; cmd_status "$@" ;;
  sync)          shift; cmd_sync "$@" ;;
  forget)        shift; cmd_forget "$@" ;;
  ""|-h|--help)  sed -n '2,26p' "$0" | sed 's/^# \?//' ;;
  *)             die "unknown command: $1 (try: mem --help)" ;;
esac
