# Pi and DeepSeek memory regression

This opt-in runner uses the actual Pi 0.83.0 SDK, the extension and skill
installed by the selected Mnemon executable, and DeepSeek's `deepseek-flash`
model. It is separate from the deterministic Go gate and the Agency live
scenarios. No paid request runs during `make test`.

Use Node.js 22.19.0 or newer, Python 3, and the pinned dependency:

```sh
npm ci --ignore-scripts --no-audit --no-fund --prefix test/memory/pi
go build -o mnemon .
PI_MEMORY_PACKAGE_DIR="$(pwd)/test/memory/pi/node_modules/@earendil-works/pi-coding-agent" \
  MNEMON_BIN="$(pwd)/mnemon" \
  python3 -m unittest discover -s test/memory/pi -p 'test_*.py'
python3 test/memory/pi/score_answers.py \
  --inputs testdata/memory/long-horizon/inputs.json \
  --oracle testdata/memory/long-horizon/oracle.json --self-test
PI_MEMORY_PACKAGE_DIR="$(pwd)/test/memory/pi/node_modules/@earendil-works/pi-coding-agent" \
  node --test internal/memory/setup/assets/pi/mnemon.test.mjs
```

The SDK lifecycle test uses a deterministic offline provider to verify prompt
growth and compaction behavior. Its output is not a model quality score.
The helper suite also makes an actual Pi request to a local 503 server, checks
the selected endpoint and loaded resources, and verifies cleanup without
reporting a memory score. That boundary case is skipped if its two environment
variables are omitted.

First inspect prepared development inputs without contacting a provider:

```sh
python3 test/memory/pi/run_live.py \
  --inputs testdata/memory/long-horizon/inputs.json --split dev \
  --binary ./mnemon --output tmp/pi-dev-prepared --prepare-only
```

Run live evaluation in a new output directory. The runner prompts for the key
with terminal echo disabled; an existing `DEEPSEEK_API_KEY` is also accepted
and removed from child environments. It passes the key to Node only over stdin
and keeps it in Pi's in-memory credential store. Do not put a key in command
arguments or a checked-in configuration file.

```sh
python3 test/memory/pi/run_live.py \
  --inputs testdata/memory/long-horizon/inputs.json --split dev \
  --binary ./mnemon --output tmp/pi-dev-live --live
python3 test/memory/pi/score_answers.py \
  --inputs testdata/memory/long-horizon/inputs.json \
  --oracle testdata/memory/long-horizon/oracle.json --split dev \
  --predictions tmp/pi-dev-live/answers.json --output tmp/pi-dev-live/scores.json
```

For the first holdout, select `--split holdout` and a fresh output directory.
For the eight selected official cases, use
`testdata/memory/long-horizon/official/inputs.json` and its separate
`oracle.json`. The runner never reads an answer oracle. Case category, split,
and answer-derived source annotations are not sent to Pi. See the
[fixture provenance and scoring contract](../../../testdata/memory/long-horizon/README.md)
before interpreting a score.

The conversation mode imports every source turn with equal importance into an
isolated store, preserving speaker, date, source, and turn IDs. Each question
starts a fresh Pi session over that case's store. Embeddings use an unavailable
loopback endpoint, making this explicitly a fallback-retrieval evaluation;
it does not measure a configured embedding provider. Pi may make multiple
focused CLI calls, but cannot read the seed file, gold, unrelated files, or
other stores. Read-only questions cannot modify memory. Keep this mode separate
from acquisition: it checks retrieval and answers after lossless raw-turn
import, not how well a model selects facts to remember.

Exercise normal model-selected memory writes and historical corrections with:

```sh
python3 test/memory/pi/run_live.py \
  --inputs testdata/memory/pi-lifecycle/inputs.json \
  --binary ./mnemon --output tmp/pi-acquisition-live --live
```

This interaction mode preserves each original prompt and returns the actual
tool transcript plus a bounded, independent final SQLite snapshot. Inspect
the [acquisition acceptance criteria](../../../testdata/memory/pi-lifecycle/README.md).
It does not produce structured answer scores.

Each case uses a private temporary Mnemon and Pi directory. Only the installed
Mnemon extension and skill are loaded. The default model uses high reasoning,
at most 8,192 output tokens per request, and a 90-second prompt deadline.
Tool and case limits bound work. Provider errors abort the batch and create an
incomplete result, with no answer-accuracy claim. A malformed completed model
answer is instead a scored answer failure. Preserve the first result and retry
into a new directory; never overwrite failed attempts.

`--prompt-timeout-seconds` accepts 1–900 seconds;
`--run-timeout-seconds` sets an overall process deadline. `--pi-root` selects
another directory containing the same pinned `node_modules` installation.
`--keep-scratch` retains the private databases for additional read-only checks;
otherwise they are removed after the run. `--provider-base-url` is only for
an explicitly selected DeepSeek endpoint and is recorded in the result. It
does not change the model; credentials, query strings, and fragments in the
URL are rejected. Loopback HTTP is allowed for transport boundary tests.

For a deliberate long-wait acceptance run, select `--prompt-timeout-seconds 900`
for both binaries. DeepSeek documents a
[keep-alive wait of up to ten minutes before inference](https://api-docs.deepseek.com/quick_start/rate_limit/).
The default remains 90 seconds for bounded routine runs. A prompt deadline
covers all requests and tools for that turn, including queue time; exceeding it
does not establish whether the model would eventually answer correctly.

To compare two implementations, build both executables from recorded commits
in separate worktrees and invoke this same runner with the same fixture,
model, SDK, and settings. Save binary and input hashes, raw tool traces, usage,
and scores. Separate provider failures, answer correctness, evidence coverage,
write durability, and context size; none is a substitute for the others.

Generate longer inputs or run a CLI-only diagnostic with `add_filler.py` and
`probe_retrieval.py`; commands and limitations are in the fixture README. The
30/120/500 filler scales are controlled stress cases. They are neither full
LoCoMo runs nor LongMemEval S/M benchmark scores.
