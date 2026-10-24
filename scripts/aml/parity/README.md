# Parity replay

`replay.py` checks the claim the serving hardening was built on: with no fault, every Add and
every Search of `mnemon memory-agent aml-serve` answers byte for byte what the baseline build
(`51cc7da5`, before the hardening) answers to the same requests. The same request sequence, over
HTTP, goes to two builds, one after the other, and every response body is saved and compared:

| | Build | Mode | Providers |
|---|---|---|---|
| old | the baseline, `51cc7da5` | `--consolidate-on-add` | live; every model reply is recorded in the model cache |
| new | the build under test | `--consolidate background`, an admin listener on loopback | none: the cache answers every model call |

Both run the shipped BM25-only configuration (`deploy/aml/memory-config.industrial.json`), changed
only in its budget file, its caps and its model cache. The requests are two conversations: LoCoMo
conversation 0 and BEAM-128K `beam128k-1`, each as the AML platform sends it (Adds split at 20
messages or 2,000 words, the last Add retried with its `request_id`, then every question with
`top_k` 100, four at a time).

**The new build must make 0 paid calls.** Its model calls are the baseline's, so each is a cache
hit; a call that misses the cache means the new build sends a request the baseline did not, and
the replay fails. That is the point: the check is not "the answers look alike" but "the same
requests, to the same models, give the same bytes".

The script is Python 3.9 or later with the standard library only; run it as `python3 -I`.

## What you need

- **Two builds of mnemon,** each from a `git archive` of its commit, never a working tree, so that
  the binary is the commit's:

  ```sh
  src=$(mktemp -d)
  git archive <commit> | tar -x -C "$src"
  ( cd "$src" && env GOPROXY=off GOFLAGS=-mod=readonly go build -o mnemon . )   # the modules must be in the local cache
  ```

- **The datasets,** six JSON files in one directory (`--inputs`), under these names:

  | File | Holds |
  |---|---|
  | `locomo10.json` | the LoCoMo conversations (`[i].conversation` with `speaker_a`, `session_N` and `session_N_date_time`) |
  | `beam-128k.cases.json` | the BEAM-128K cases (`id`, and `sessions` of `id`, `iso` and `turns` of `speaker` and `text`) |
  | `locomo-0.questions.json` | `{"cases": [{"id", "input": {"query"}}]}`: the questions asked of conversation 0 |
  | `locomo-sample.json` | `{"ids": [...]}`: the questions of the default sample, marked in the report |
  | `beam128k-1.questions.json` | `{"questions": [{"id", "date", "question"}]}`: the questions asked of `beam128k-1` |
  | `beam-sample.json` | `{"ids": [...]}`: likewise |

  The datasets and the model cache are not in this repository.

- **A task directory** (`--root`), empty to begin with. Everything the script writes goes there:

  ```
  ROOT/memory-config.json     the configuration both builds ran on (a later run refuses a different one)
  ROOT/private/               0700: model-cache.db and model-budget.db (the model replies and the metering)
  ROOT/old/                   the baseline's run: manifest.json, responses/, data/, aml-serve.log
  ROOT/<label>/               the build under test's run, with compare-report.json
  ROOT/report.json            the comparison of a live pair (the compare stage)
  ```

## Record the baseline (once; it spends money)

The baseline is run against the live providers, with their keys in the environment of the command
(the script copies them into the server's environment and nowhere else):

```sh
export DEEPSEEK_API_KEY=... TYPESAFE_API_KEY=...     # not in a file of this repository, not in the shell's history
python3 -I scripts/aml/parity/replay.py --root "$ROOT" --inputs "$INPUTS" \
    run old --binary "$BASELINE" --built-from 51cc7da5
```

It is capped at DeepSeek CNY 15 and JEV USD 5 (the budget file and the configuration's caps), and
stops itself, killing the server, when either account has spent and held 80% of its cap, or at the
first fault in the baseline's log. The final run's baseline made 1,727 paid calls, which the cache
holds as 1,727 replies (about 1.7 MB). Keep `ROOT/old/` and `ROOT/private/`: they are the baseline
for every later replay, which is why they are kept out of the repository (they hold the replies,
which quote the evidence they were given).

`scrub` then looks for the two keys in every file under the task directory and prints file names
only; none should hold one:

```sh
python3 -I scripts/aml/parity/replay.py --root "$ROOT" scrub
```

## Replay the build under test (free)

```sh
python3 -I scripts/aml/parity/replay.py --root "$ROOT" --inputs "$INPUTS" \
    replay my-build --binary "$NEWBUILD" --built-from <commit>
```

This runs the new build over the whole sequence and compares it with `ROOT/old`. The server is
given what makes it safe to run anywhere:

- **placeholder provider keys:** the real ones are never read, so the server could not
  authenticate to a provider even if it tried;
- **dead proxies:** every proxy variable points at a loopback port nothing listens on and
  `NO_PROXY` is cleared, so that a cache miss cannot leave the machine. It fails unsent, shows as a
  budget attempt (released) and as a degraded Search, and the comparison fails.

It prints the result as JSON, last, and writes it to `ROOT/my-build/compare-report.json`. The verdict:

| Verdict | Means |
|---|---|
| `PASS` | every Add and Search body identical to the baseline's, the same requests (by digest), the consolidated stores identical but for the cache's hit counter, 0 paid calls, 0 faults in the new build's log |
| `FAIL` | any difference, any paid call, any fault; `differences` says where (the first differing byte, and for a Search the items) |
| `INCOMPLETE` | one of the runs was aborted |

The exit status is 0 for `PASS` and 1 otherwise. A replay adds nothing to the model cache or the
budget file: `cacheBefore` equals `cacheAfter`, and `budgetAttemptsBefore` equals
`budgetAttemptsAfter`, in the report's `replay` object.

**What a replay may replace.** The label names the run's directory, `ROOT/my-build`, and `--force`
replaces that directory. A label is one plain name (letters, digits, `.`, `_` and `-`, starting
with a letter or a digit), and the replay, like `run new`, never replaces what the task directory
must keep: the baseline (`ROOT/old`, or the run `--old` names), `ROOT/private` (the model cache and
the budget file), the build output (`bin`, `src`), the `--dry` runs (`dry`), the configuration and
the report, or a directory that holds one of them. It compares the directories, not the labels as
typed, so `old/`, `./old`, `old/.`, `.`, `..`, a link to `old` and, on a file system that folds
case, `OLD` are refused as `old` is. It checks every input (the build, the datasets, the keys, the
configuration) before it removes anything, so a run that cannot start leaves the task directory as
it was. (`run old --force` records the baseline again, and is the one command that replaces
`ROOT/old`.)

**What it caught and what it proved.** For the final fixes of the serving hardening, `replay` on
the build of that commit gave `PASS`: 152 of 152 Searches of `locomo-0` and 20 of 20 of
`beam128k-1` identical, 28 of 28 and 37 of 37 Adds, and the retried Add of each (239 requests in
all), both users' consolidated state identical, 0 paid calls, 1,727 replies and 1,727 budget
attempts before and after.

## When the replay fails, or the product changes

- **Paid calls above 0 means the requests changed.** A change to a prompt, a model setting, the
  View, the consolidation rules or anything else that alters a request a model receives misses the
  cache, by design (the key is a SHA-256 over the provider, the endpoint, the model and the encoded
  body). That is a change of behaviour: look at it, and if it is meant, record a new baseline
  (a new `ROOT`) from a baseline build that has the change.
- **A difference without paid calls** is a change in what the service answers for the same model
  replies: the compare report names the first differing byte and the items.
- The `--dry` flag runs every stage with local models that fail at once, in `ROOT/dry`, with no
  keys: a plumbing test of the script and the builds, which spends nothing. Its verdict is `FAIL`
  (every Search degrades, and the log says so); what it shows is that the bodies of two builds
  compare and the stages run.

## The other stages

| Stage | What it does |
|---|---|
| `plan` | counts the requests of each user (Adds, retries, Searches, bytes) and hashes the sequence, so that two machines can check they send the same one; no server |
| `smoke --old-binary B --new-binary B` | starts each build on throwaway paths and makes no request: the flags, the configuration and the admin listener are accepted |
| `run new --binary B` | the new build against the live providers, as the baseline is run (`--stall-ms` and `--probe-ms` add the supplementary stall and queue probe of the first verification) |
| `compare --old L --new L` | compares two runs already made, and records the facts about the task (builds, configuration, dataset hashes, cache, budget) in `ROOT/report.json` |
| `scrub` | looks for the provider keys of the environment in every file under `ROOT`, naming files, never values |

`--config FILE` runs both builds on another memory configuration (it must be BM25 only); without
it the script uses `deploy/aml/memory-config.industrial.json` of the repository it is in.
