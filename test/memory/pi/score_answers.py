#!/usr/bin/env python3
"""Deterministic answer/provenance scores. Never invokes an LLM or the network."""
import argparse
import copy
import json
import math
import unicodedata
from pathlib import Path


def normalized(value):
    return unicodedata.normalize('NFC', value).strip().casefold()


def equivalent(actual, expected):
    if expected is None or isinstance(expected, bool):
        return type(actual) is type(expected) and actual == expected
    if isinstance(expected, str):
        return isinstance(actual, str) and normalized(actual) == normalized(expected)
    if isinstance(expected, (int, float)):
        return (type(actual) in (int, float) and math.isfinite(actual)
                and actual == expected)
    if isinstance(expected, list):
        return (isinstance(actual, list) and len(actual) == len(expected)
                and all(equivalent(a, e) for a, e in zip(actual, expected)))
    raise ValueError(f'Unsupported expected value: {type(expected).__name__}')


def validate_fixture(inputs, oracle):
    seen_cases, seen_turns, seen_questions = set(), set(), set()
    for case in inputs['cases']:
        assert case['id'] not in seen_cases, case['id']
        seen_cases.add(case['id'])
        tids = [turn['id'] for session in case['sessions'] for turn in session['turns']]
        assert len(tids) == len(set(tids)), case['id']
        assert not (set(tids) & seen_turns), case['id']
        seen_turns.update(tids)
        for question in case['questions']:
            qid = question['id']
            assert qid not in seen_questions, qid
            seen_questions.add(qid)
            gold = oracle[qid]
            assert set(question['answer_slots']) == set(gold['slots']), qid
            assert set(gold['evidence_turn_ids']) <= set(tids), qid
            assert type(gold['abstain']) is bool, qid
            assert not gold['abstain'] or all(v is None for v in gold['slots'].values()), qid
    assert seen_questions <= set(oracle), 'missing oracle question IDs'


def evaluate(inputs, oracle, predictions, split=None):
    validate_fixture(inputs, oracle)
    details = []
    selected = [c for c in inputs['cases'] if split is None or c['split'] == split]
    all_question_ids = {q['id'] for c in inputs['cases'] for q in c['questions']}
    for case in selected:
        valid_turn_ids = {t['id'] for s in case['sessions'] for t in s['turns']}
        for question in case['questions']:
            qid = question['id']
            gold = oracle[qid]
            answer = predictions.get(qid)
            errors = []
            if not isinstance(answer, dict):
                answer = {}
                errors.append('missing_or_non_object_answer')
            if set(answer) != {'slots', 'evidence_turn_ids', 'abstain'}:
                errors.append('answer_keys')
            slots = answer.get('slots')
            slots_valid = isinstance(slots, dict) and set(slots) == set(gold['slots'])
            if not slots_valid:
                errors.append('slot_keys')
            slots = slots if isinstance(slots, dict) else {}
            slot_scores = {}
            for key, expected in gold['slots'].items():
                alternatives = [expected] + gold.get('aliases', {}).get(key, [])
                slot_scores[key] = (key in slots and any(equivalent(slots[key], value)
                                                        for value in alternatives))
            abstain_valid = type(answer.get('abstain')) is bool
            if not abstain_valid:
                errors.append('abstain_type')
            abstain_correct = abstain_valid and answer['abstain'] == gold['abstain']
            answer_correct = slots_valid and all(slot_scores.values()) and abstain_correct
            citations = answer.get('evidence_turn_ids')
            citations_valid = (isinstance(citations, list)
                               and all(isinstance(value, str) for value in citations))
            if not citations_valid:
                errors.append('evidence_type')
                citations = []
            if len(citations) != len(set(citations)):
                errors.append('duplicate_evidence_id')
            unknown = sorted(set(citations) - valid_turn_ids)
            if unknown:
                errors.append('unknown_evidence_id')
            required = set(gold['evidence_turn_ids'])
            matched = required & set(citations)
            coverage = len(matched) / len(required) if required else None
            # The fixed evidence set is canonical, not an LLM proof checker.
            # Abstention has no positive gold evidence: score citations only for provenance.
            supported = (not required or required <= set(citations)) and not unknown and citations_valid
            details.append(dict(question_id=qid, case_id=case['id'], split=case['split'],
                                category=case['category'], answer_correct=answer_correct,
                                slot_scores=slot_scores, abstain_correct=abstain_correct,
                                format_valid=not errors, canonical_evidence_coverage=coverage,
                                canonical_evidence_complete=supported,
                                unknown_evidence_ids=unknown, errors=errors,
                                strict_pass=answer_correct and not errors and supported))

    def aggregate(rows):
        n = len(rows)
        evidence_rows = [r for r in rows if r['canonical_evidence_coverage'] is not None]
        return dict(questions=n, answer_correct=sum(r['answer_correct'] for r in rows),
                    answer_accuracy=sum(r['answer_correct'] for r in rows) / n if n else None,
                    strict_pass=sum(r['strict_pass'] for r in rows),
                    format_valid=sum(r['format_valid'] for r in rows),
                    macro_canonical_evidence_coverage=(sum(r['canonical_evidence_coverage'] for r in evidence_rows)
                                                       / len(evidence_rows) if evidence_rows else None))

    return dict(schema_version='1', summary=aggregate(details),
                by_split={key: aggregate([r for r in details if r['split'] == key])
                          for key in sorted({r['split'] for r in details})},
                by_category={key: aggregate([r for r in details if r['category'] == key])
                             for key in sorted({r['category'] for r in details})},
                unexpected_question_ids=sorted(set(predictions) - all_question_ids), details=details)


def load_predictions(path):
    raw = Path(path).read_text(encoding='utf-8')
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        value = [json.loads(line) for line in raw.splitlines() if line.strip()]
    if isinstance(value, dict):
        return value
    if isinstance(value, list):
        result = {}
        for row in value:
            qid = row['question_id']
            if qid in result:
                raise ValueError(f'Duplicate prediction: {qid}')
            result[qid] = {k: v for k, v in row.items() if k != 'question_id'}
        return result
    raise ValueError('Predictions must be a question-ID map or JSONL objects with question_id')


def self_test(inputs, oracle):
    perfect = {qid: {k: copy.deepcopy(gold[k]) for k in ('slots', 'evidence_turn_ids', 'abstain')}
               for qid, gold in oracle.items()}
    result = evaluate(inputs, oracle, perfect)
    assert result['summary']['strict_pass'] == len(oracle)
    checks = ['gold_passes']
    mutations = [
        ('boolean_is_not_number', 'dev04.q1', 'production_allowed', 0),
        ('wrong_date_fails', 'hold03.q1', 'signed_date', '2026-03-08'),
        ('same_name_fails', 'hold01.q1', 'extension', '684'),
        ('guess_on_abstention_fails', 'hold07.q1', 'confirmation_code', 'MH-593'),
        ('array_order_is_explicit', 'hold10.q1', 'retained_kits', ['KIT-C', 'KIT-A']),
    ]
    for label, qid, slot, value in mutations:
        altered = copy.deepcopy(perfect)
        altered[qid]['slots'][slot] = value
        row = next(r for r in evaluate(inputs, oracle, altered)['details'] if r['question_id'] == qid)
        assert not row['answer_correct'], label
        checks.append(label)
    altered = copy.deepcopy(perfect)
    altered['dev01.q1']['evidence_turn_ids'] = ['hold01.s1.t1']
    row = evaluate(inputs, oracle, altered)['details'][0]
    assert row['answer_correct'] and not row['strict_pass'] and row['unknown_evidence_ids']
    checks.append('foreign_scope_citation_fails_provenance_only')
    altered = copy.deepcopy(perfect)
    altered['dev03.q1']['evidence_turn_ids'] = ['dev03.s4.t1']
    row = next(r for r in evaluate(inputs, oracle, altered)['details'] if r['question_id'] == 'dev03.q1')
    assert row['strict_pass'] and row['canonical_evidence_coverage'] is None
    checks.append('absence_citation_allowed')
    altered = copy.deepcopy(perfect)
    altered['hold09.q1']['slots']['buyer'] = 'Алина Соколова'
    row = next(r for r in evaluate(inputs, oracle, altered)['details'] if r['question_id'] == 'hold09.q1')
    assert row['answer_correct']
    checks.append('explicit_alias_accepted')
    return dict(passed=len(checks), checks=checks, cases=len(inputs['cases']), questions=len(oracle))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', type=Path, required=True)
    parser.add_argument('--oracle', type=Path, required=True)
    parser.add_argument('--predictions', type=Path)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--split', choices=['dev', 'holdout'])
    parser.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    inputs = json.loads(args.inputs.read_text(encoding='utf-8'))
    oracle = json.loads(args.oracle.read_text(encoding='utf-8'))
    if args.self_test:
        result = self_test(inputs, oracle)
    elif args.predictions:
        result = evaluate(inputs, oracle, load_predictions(args.predictions), args.split)
    else:
        parser.error('--predictions or --self-test required')
    raw = json.dumps(result, ensure_ascii=False, indent=2) + '\n'
    if args.output:
        args.output.write_text(raw, encoding='utf-8')
    print(raw, end='')


if __name__ == '__main__':
    main()
