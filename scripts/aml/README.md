# AML verification scripts

These scripts check `mnemon memory-agent aml-serve` under faults and load with no real model
provider. `fakeprovider/` stands in for DeepSeek and JEV on loopback, and every model's
`testEndpoint` points at it, so no paid call can be made. They need bash (3.2 or later), Go,
curl, jq and awk.

| File | What it does |
|---|---|
| `final.sh` | The final verification run in one command (below) |
| `drill.sh` | The fault drill: each scenario against a steady Add and Search workload, judged both ways |
| `load.sh` | The load test: Adds, Searches or both at rising rates, and the knee of each, locally or against a base URL |
| `fakeprovider/` | The fake DeepSeek and JEV: deterministic replies, and faults set at run time through a loopback-only `POST /_fault` |
| `lib.sh`, `scenario.jq`, `knee.jq` | What the scripts share (sourced), the drill's criteria for one run of a scenario, and the knee rule (jq programs) |

Exit status of every script: **0** when the run completed and its hard criteria held, **1** when
a hard criterion failed or a secret reached the output, **2** when the run could not be made (a
usage error, a tool or a server that failed, an interruption). These are the scripts' own codes,
never a tool's: an option without its value, or a failure before a run starts, is a 2. `load.sh`
has no hard criteria: its knee is a measurement.

## The final run

Run it on a quiet machine, once the product is final:

```sh
scripts/aml/final.sh            # or: --rev <commit>, --repeat <n>, --compare <file>
```

It takes about two hours:

1. It extracts the commit (default `HEAD`) with `git archive` and builds mnemon from that, never
   from the working tree.
2. It runs that tree's own `drill.sh` (every scenario) and `load.sh --local`: Searches at 0.25 to
   3 a second in 300 s steps, then Adds at 1 to 32 a second in 60 s steps.
3. It writes them into a new dated directory, `.testdata/aml/final-<UTC time>/`, with `drill/`,
   `load-search/`, `load-add/` and `summary.md`.
4. It prints, and writes to `summary.md`:
   - the drill's matrix;
   - the drill against an earlier run, scenario by scenario. The earlier run defaults to
     `.testdata/aml/drill-task16/task16-matrix.json`.
   - the knees with their p50 and p95, which give the capacity table of `deploy/aml/README.md`.

## The drill

```sh
scripts/aml/drill.sh [--only a,b] [--repeat 3] [--binary path/to/mnemon --built-from <commit>]
```

**What it runs.**
- It builds the fake provider. Without `--binary` it also builds mnemon from this tree.
- It starts toxiproxy v2.12.0 through `go run`, but only when a selected scenario needs it.
- It starts aml-serve with background consolidation, the dedicated limits and an admin token,
  then runs the table in `drill.sh`.

**The scenario table.** Each scenario states its expectations as numbers: the fewest Searches,
the signal that shows its fault took effect, the bounds on degraded and partial Searches, the
Search p95, the breaker's behaviour (`cycle`: it opens during the fault and closes within 180 s
of its end; `steady`: it never opens, with samples to show it), and retries. The drill checks
every row before it starts anything, and refuses the run (2) if a column holds a value it could
not judge, such as `=0` for `0`.

**The bounds on degraded and partial Searches** are `0`, `>0`, a number, or `-` (not judged). The
scenarios whose fault is random or holds over a window have bounds fixed in advance from the
retry policy (ruling R82), each derived in the table:
- `P^A`, a fault random per attempt at rate `P`, which the policy gives `A` attempts (2 for an
  invalid reply, retried once; 4, the Interactive attempts, for resets, 5xx, spikes and stalls):
  at most max(1, ⌈N × min(1, 2·10·P^A)⌉) of the N Searches a run answers. A Search makes about 10
  model calls; the 2 is a margin for chance. A broken retry layer still fails it.
- `window+45`, a fault over a window: at most the Searches that started in the window or in the
  45 s after it (the Interactive deadline), and so none of those that started before it.

**Criteria every scenario must meet:**
- no non-2xx answer, other than deliberate 503 sheds;
- the backlog drained within 600 s of the fault's end;
- the largest backlog below 10,500 characters once drained: an idle user may hold up to a batch,
  10,000 characters, and the backlog's estimate also counts the records' headers, which a batch
  does not; 5% covers them (ruling R78);
- no reservation left held;
- no "without JEV" line.

**How the criteria fail a run.**
- A hard or expected scenario fails the run on any criterion.
- A measured one fails it only on validity: too few Searches, a fault that never took effect,
  a non-2xx answer, a held reservation, or a link fallback. Its other criteria are reported.
- A run with `--only` is a partial run, and its `report.json` and summary say so.

**Output.** It writes `report.json` and `matrix.md`. The matrix shows the degraded and partial
Searches beside their bounds, `throttled`, `invalid` and `rejected` (by kind) in columns of their
own, and the other outcomes of `mnemon_aml_model_attempts_total` in one. Each run's bounds, with
what they were derived from, are in its `bounds` in `report.json`.

**Test hooks.**
- `AML_DRILL_SCENARIOS=FILE` runs a table of your own (lines starting with `#` are comments).
- `AML_DRILL_RESIDUAL_LIMIT=N` sets the residual-backlog limit.
- `AML_DRILL_FAIL_AT=fault` fails the run just after the first fault is applied.

## The load test

```sh
scripts/aml/load.sh --local [--binary path/to/mnemon] --mode search --rates 0.25,0.5,1,1.5,2,3
scripts/aml/load.sh --base-url https://host --key-file key --admin-url http://10.77.0.5:8091 \
  --admin-token-file token --mode add --rates 1,2,4,8
```

**The key and the token.**
- A key or token file must be private (mode 0600).
- It may hold the bare secret, or the instance's line `AML_API_KEY=…` or `MNEMON_ADMIN_TOKEN=…`.
- `--base-url` must hold no `user:password@`.

**The method of the knee:**
- Search steps last at least 300 s. `--validate` runs shorter ones and reports no knee.
- The scheduler is settled before each step, the first included, and each step records whether it
  started settled. This needs the admin listener, and without it there is no Add knee. An Add
  step that did not start settled is void, and so is every step after it: the Add knee comes from
  the steps before it.
- The baseline (the lowest rate) needs at least 50 answered requests.
- The knee is the highest rate whose p95 and p50 both stay within twice the baseline's, with no
  non-2xx answer, every lower rate holding too.

## Safety

- **Secrets.** Every key and token lives in a 0600 file in a private temporary directory.
  - curl gets it only on its standard input (`--config -`), vegeta in its targets on a pipe, and
    aml-serve through its environment.
  - It is never an argument.
  - Each script greps its own output for the secrets it used and fails on a match.
- **The fake provider's control endpoint** answers loopback clients only.
- **Cleanup.** A failed run stops its workload, sampler and servers before it removes anything;
  `load.sh` stops its preparation's requests and its attacks too. The tests in
  `test/deploy/aml_scripts_test.go` check that no process of a drill or a load test failed midway
  survives (`AML_LOAD_FAIL_AT=prepare` or `step` fails a load test on purpose).
