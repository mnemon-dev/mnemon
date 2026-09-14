#!/usr/bin/env python3
"""Raw-question recall@10 probe on isolated SQLite stores; no model/provider.

No query rewriting, hand-authored entities/edges, evidence-aware importance,
or gold filtering is used. Oracle data is read only after CLI outputs exist.
"""
import argparse
import hashlib
import json
import os
import re
import subprocess
import time
from datetime import datetime, timezone
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[3]
FIXTURES = REPO_ROOT / 'testdata' / 'memory' / 'long-horizon'


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, default=REPO_ROOT / 'mnemon')
    parser.add_argument('--inputs', type=Path, nargs='+',
                        help='Existing stress-N-inputs.json files; default generates the frozen three noise scales')
    parser.add_argument('--core-inputs', type=Path, default=FIXTURES / 'inputs.json')
    parser.add_argument('--oracle', type=Path, default=FIXTURES / 'oracle.json')
    parser.add_argument('--cases', nargs='+', default=['hold02', 'hold04', 'hold09'],
                        help='Cases to expand when --inputs is omitted')
    parser.add_argument('--scales', type=int, nargs='+', choices=[30, 120, 500], default=[30, 120, 500])
    parser.add_argument('--seed', default='mnemon-memory-regression-v1')
    parser.add_argument('--output', type=Path,
                        default=REPO_ROOT / 'tmp' / ('recall-probe-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')),
                        help='New or empty result directory; defaults to an isolated directory under repo tmp/')
    args = parser.parse_args()
    binary = args.binary.resolve()
    if not binary.is_file():
        parser.error('binary does not exist; run go build -o mnemon . or pass --binary')
    output = args.output.resolve()
    if output.exists() and any(output.iterdir()):
        parser.error('output directory must be new or empty; preserve prior observations')
    output.mkdir(parents=True, exist_ok=True)
    if args.inputs is None:
        from add_filler import expand
        core_inputs = json.loads(args.core_inputs.read_text(encoding='utf-8'))
        args.inputs = []
        for scale in args.scales:
            expanded = expand(core_inputs, scale, set(args.cases), args.seed)
            path = output / f'stress-{scale}-inputs.json'
            path.write_text(json.dumps(expanded, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
            args.inputs.append(path)
    data_dir = output / 'scratch-data'
    data_dir.mkdir()
    # A fresh, explicit environment avoids reading or forwarding inherited keys.
    env = {'PATH': os.defpath, 'LANG': 'C.UTF-8',
           'MNEMON_DATA_DIR': str(data_dir), 'MNEMON_EMBED_ENDPOINT': 'http://127.0.0.1:1',
           'MNEMON_EMBED_PROTOCOL': 'ollama', 'MNEMON_MAX_INSIGHTS': '0'}
    summary = dict(kind='raw-question canonical-evidence recall@10 probe',
                   not_a_pi_or_formal_benchmark_score=True,
                   started_utc=datetime.now(timezone.utc).isoformat(),
                   binary=str(binary), binary_sha256=sha(binary),
                   input_hashes={str(p): sha(p) for p in args.inputs},
                   settings=dict(embedding='unavailable loopback endpoint, no inherited credentials',
                                 auto_pruning=False, importance=3, category='context',
                                 explicit_edges=False, explicit_entities=False,
                                 recall_readonly=True, query_rewriting=False, limit=10),
                   records=[])

    def call(store, suffix, command):
        argv = [str(binary), '--data-dir', str(data_dir), '--store', store] + command
        started = time.monotonic()
        result = subprocess.run(argv, env=env, cwd=output, capture_output=True, text=True, timeout=180)
        (output / f'{store}.{suffix}.stdout.json').write_text(result.stdout, encoding='utf-8')
        (output / f'{store}.{suffix}.stderr.txt').write_text(result.stderr, encoding='utf-8')
        meta = dict(argv=argv, exit_code=result.returncode, seconds=time.monotonic() - started)
        (output / f'{store}.{suffix}.command.json').write_text(json.dumps(meta, indent=2) + '\n')
        if result.returncode:
            raise RuntimeError(f'{store} {suffix}: nonzero exit {result.returncode}; inspect saved stderr')
        return json.loads(result.stdout), meta

    for input_file in args.inputs:
        cases = json.loads(input_file.read_text(encoding='utf-8'))['cases']
        scale = re.search(r'stress-(\d+)-', input_file.name)
        if not scale:
            raise ValueError('Expected frozen stress-N-inputs.json filename')
        noise = int(scale.group(1))
        for case in cases:
            store = f'noise{noise}-{case["id"]}'
            insights, turn_ids = [], []
            for session in case['sessions']:
                for turn in session['turns']:
                    content = (f'[session_id={session["id"]}; turn_id={turn["id"]}; '
                               f'date_time={session["date_time"]}; speaker={turn["speaker"]}]\n{turn["text"]}')
                    insights.append(dict(content=content, category='context', importance=3,
                                         source='raw-regression-turn', created_at=session['date_time']))
                    turn_ids.append(turn['id'])
            draft_path = output / f'{store}.draft.json'
            draft_path.write_text(json.dumps(dict(schema_version='1', source='raw-regression-turn',
                                                   insights=insights), ensure_ascii=False, indent=2) + '\n')
            imported, import_meta = call(store, 'import', ['import', str(draft_path)])
            assert imported['errors'] == 0, store
            assert imported['imported'] == len(insights), store
            assert imported['skipped'] == 0 and imported['auto_pruned'] == 0, store
            id_map = {entry['id']: turn_ids[entry['index']] for entry in imported['results']}
            (output / f'{store}.insight-turn-map.json').write_text(json.dumps(id_map, indent=2) + '\n')
            for question in case['questions']:
                response, recall_meta = call(store, question['id'],
                                              ['--readonly', 'recall', question['text'], '--limit', '10', '--verbose'])
                returned = response.get('results') or []
                recalled = [id_map[result['insight']['id']] for result in returned]
                summary['records'].append(dict(case_id=case['id'], question_id=question['id'],
                                                filler_records=noise, imported_turns=len(insights),
                                                import_seconds=import_meta['seconds'], recall_seconds=recall_meta['seconds'],
                                                recalled_turn_ids=recalled, recall_meta=response.get('meta', {})))
            print(json.dumps(dict(store=store, imported=len(insights), questions=len(case['questions']))), flush=True)
    # Scoring occurs only after every raw retrieval trace has been written.
    oracle = json.loads(args.oracle.read_text(encoding='utf-8'))
    summary['oracle_sha256'] = sha(args.oracle)
    for record in summary['records']:
        required = set(oracle[record['question_id']]['evidence_turn_ids'])
        found = required & set(record['recalled_turn_ids'])
        record.update(canonical_evidence_turn_ids=sorted(required), matched_evidence_turn_ids=sorted(found),
                      evidence_coverage=len(found) / len(required) if required else None,
                      complete_evidence=required <= set(record['recalled_turn_ids']))
    summary['by_noise'] = {}
    for noise in sorted({r['filler_records'] for r in summary['records']}):
        rows = [r for r in summary['records'] if r['filler_records'] == noise]
        num = sum(len(r['matched_evidence_turn_ids']) for r in rows)
        den = sum(len(r['canonical_evidence_turn_ids']) for r in rows)
        summary['by_noise'][str(noise)] = dict(questions=len(rows), matched_evidence=num, required_evidence=den,
                                              micro_coverage=num / den,
                                              macro_coverage=sum(r['evidence_coverage'] for r in rows) / len(rows),
                                              complete_questions=sum(r['complete_evidence'] for r in rows))
    summary['completed_utc'] = datetime.now(timezone.utc).isoformat()
    if sha(binary) != summary['binary_sha256']:
        raise RuntimeError('binary changed during the probe; preserve logs and rerun with a stable binary')
    (output / 'summary.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(summary['by_noise'], indent=2), flush=True)


if __name__ == '__main__':
    main()
