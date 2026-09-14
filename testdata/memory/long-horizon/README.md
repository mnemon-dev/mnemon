These fixtures exercise long-term conversational memory with separate model inputs and deterministic answer oracles. They are selected regression cases, not a reproduction of a complete public benchmark or an estimate of its overall accuracy.

`inputs.json` contains original scenarios written for this regression: 4 development cases with 5 questions and 12 holdout cases with 14 questions. Each case has four dated sessions, with 64 sessions and 128 turns overall. The cases cover cross-session chains, same-name isolation, relative dates, effective-date updates, negation across predicates, retractions, missing evidence, speaker authority, multilingual aliases, refunds and duplicate receipts, conditional permissions, and requirements-based reasoning.

`oracle.json` contains expected scalar/array slots, explicit aliases, canonical supporting turn IDs, and abstention flags. These files were frozen on 2026-09-15 before holdout runs:

| File | SHA256 |
|---|---|
| `inputs.json` | `174534e71d156c312e9ca09f68bce2bffa8aa193b4f781796ce5c97146c2184c` |
| `oracle.json` | `7176cd1becc2f69cb701e38e087a1f3e7c071f4fd2e18fe6d54cc0c0fc390f12` |
| `official/inputs.json` | `74fa477a81a13a7a881dbef98b24f81bbd61e08eb51b91f81e994f31dfc29075` |
| `official/oracle.json` | `62c88cd6c605643f16df4a96aa4f116eadfb96d26e73febf2ae666d786d1f9dc` |

Keep gold, category, split, evidence labels, and answer-derived metadata out of model-visible prompts. Development cases may be used to adapt the runner. Preserve the first holdout run; if its failures inform a fix, subsequent runs are regression validation rather than a new blind estimate. Do not silently change gold to fit model answers.

The original timelines are fictional closed worlds. Interpret an explicit question date independently of the machine's current date; some fictional sessions occur after the freeze date. Original questions that need a timezone state UTC. Event time, report time, planned time, and effective time remain distinct. Preserve speaker and source turn IDs when importing raw turns, including assistant suggestions and later user corrections.

`official/` contains eight selected questions from the official cleaned **LongMemEval V1 oracle** release: 17 complete source sessions and 196 turns. Original turn content is neither summarized nor truncated. Only the selected adapted inputs and separate oracle are vendored, together with `LICENSE.txt` and `provenance.json`; the full 500-question source corpus and S/M histories are not included.

The [official dataset card](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned) specifies MIT, and the [official code repository](https://github.com/xiaowu0162/LongMemEval) also uses MIT. The accompanying license is copied from repository revision `9e0b455f4ef0e2ab8f2e582289761153549043fc`. Preserve that notice when redistributing these records. Data revision is `98d7416c24c778c2fee6e6f3006e7a073259d48f`; the downloaded [oracle source file](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/98d7416c24c778c2fee6e6f3006e7a073259d48f/longmemeval_oracle.json) has SHA256 `821a2034d219ab45846873dd14c14f12cfe7776e73527a483f9dac095d38620c`. `official/provenance.json` records original question IDs/types, evidence session IDs, `has_answer` turn positions, local ID mapping, transformations, and hashes.

The selected official IDs are `e47becba`, `4c36ccef`, `6a1eabeb`, `36b9f61e`, `gpt4_d84a3211`, `gpt4_76048e76`, `bbf86515`, and `982b5123_abs`. They cover user-side and assistant-side extraction, updated facts, cross-session aggregation with repeated mentions, temporal ordering/arithmetic, and abstention. Open-ended preference recommendations are excluded from exact slot scoring. Selection prioritized explicit evidence and an unambiguous answer, not random sampling or measured product performance.

Official inputs replace source IDs containing `answer_` or `_abs` with local IDs and remove all `has_answer` labels. Sessions are sorted by source timestamp. The original question wording is retained with its source question date and a uniform output/abstention instruction. Source timestamps do not identify a timezone: their local clock values are serialized with `Z` as a shared storage reference clock, without asserting that the source actually used UTC. Original date strings remain in provenance. These selected questions require no cross-timezone inference, and all selected history sessions precede their question timestamp.

LongMemEval V1 has distinct S, M, and oracle settings. Its [paper](https://arxiv.org/abs/2410.10813) and [official evaluator](https://github.com/xiaowu0162/LongMemEval/blob/9e0b455f4ef0e2ab8f2e582289761153549043fc/src/evaluation/evaluate_qa.py) use a model-based answer judge. The selected oracle histories plus local exact-slot scoring here are an adaptation; report them as “eight selected official oracle regression cases,” never as a full LongMemEval score. LongMemEval V2 is a separate release and is not used here.

[LoCoMo](https://github.com/snap-research/locomo) and its [paper](https://aclanthology.org/2024.acl-long.747/) informed the task taxonomy: single-hop/multi-hop recall, temporal reasoning, and adversarial unanswerable questions. LoCoMo's [CC BY-NC 4.0 license](https://github.com/snap-research/locomo/blob/3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376/LICENSE.txt) has a noncommercial restriction. No LoCoMo dialogue, QA record, image, or close paraphrase is vendored here. The original scenarios reuse task ideas only.

The response contract is `{"slots": {...}, "evidence_turn_ids": [...], "abstain": false}`. `test/memory/pi/score_answers.py` performs no model calls: slots match fixed values or declared aliases, strings use NFC/trim/casefold normalization, finite numbers compare numerically, booleans are distinct from numbers, and ordered arrays preserve the order requested by the question. Main accuracy requires every slot and the abstention flag to be correct. Format validity and canonical evidence coverage are separate metrics. A canonical evidence set is one checked sufficient set, not a claim that all other supporting citations are wrong; do not substitute its strict-pass diagnostic for answer accuracy. Unknown/cross-case citations fail provenance checks. Abstention has no positive evidence-recall denominator.

Run the deterministic checks and score captured model answers from the repository root:

```sh
python3 test/memory/pi/score_answers.py --inputs testdata/memory/long-horizon/inputs.json --oracle testdata/memory/long-horizon/oracle.json --self-test
python3 test/memory/pi/score_answers.py --inputs testdata/memory/long-horizon/inputs.json --oracle testdata/memory/long-horizon/oracle.json --predictions answers.jsonl --output scores.json
```

`test/memory/pi/add_filler.py` reproducibly adds 30, 120, or 500 independent single-turn sessions per chosen case. It uses topical templates with distinct entity codes and explicit independent scopes, without reading the oracle, the original question text, or original evidence text to compose filler. The core sessions and questions remain unchanged. The fixed default seed is `mnemon-memory-regression-v1`; a 500-record stress case is not the same as LongMemEval M's 500 full-length sessions.

`test/memory/pi/probe_retrieval.py` imports all raw turns into new isolated stores with equal importance and no supplied entities or edges, then issues each original question once through `recall --limit 10 --verbose --readonly`. It uses an unavailable loopback embedding endpoint and a fresh subprocess environment that does not forward credentials. Gold is loaded only after every CLI response is saved. The default cases are `hold02`, `hold04`, and `hold09`, expanded with all three fixed noise scales. Defaults resolve from the repository location and place results in a new ignored `tmp/recall-probe-...` directory; an explicit output directory must be new or empty.

```sh
go build -o mnemon .
python3 test/memory/pi/probe_retrieval.py
python3 test/memory/pi/probe_retrieval.py --binary ./mnemon --scales 30 120 500 --output tmp/recall-comparison
```

For a fixed-input comparison, pass `--inputs path/to/stress-30-inputs.json path/to/stress-120-inputs.json path/to/stress-500-inputs.json`. Preserve binary/input/oracle hashes and all raw outputs. This probe reports canonical evidence in the first ten results, not Pi answer quality: a Pi agent can reformulate queries or retrieve multiple times. A first-run point estimate can also vary with random insight IDs, graph construction, and tied scores. Use complete Pi traces and answer scores for end-to-end claims, and keep natural chat-driven memory extraction separate from deterministic raw-turn import.
