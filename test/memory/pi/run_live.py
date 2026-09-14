#!/usr/bin/env python3
"""Opt-in Pi/DeepSeek evaluation with the answer oracle kept outside Pi."""
import argparse
import getpass
import hashlib
import json
import os
from pathlib import Path
import signal
import shutil
import subprocess
import sys
from urllib.parse import urlsplit


DEFAULT_PROVIDER_BASE_URL = 'https://api.deepseek.com'


def provider_base_url(value):
    parsed = urlsplit(value)
    local = parsed.hostname in {'localhost', '127.0.0.1', '::1'}
    if (not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment
            or not (parsed.scheme == 'https' or (parsed.scheme == 'http' and local))):
        raise ValueError('provider base URL must be HTTPS (or loopback HTTP), without credentials, query, or fragment')
    return value.rstrip('/')


def prepare(inputs, binary, pi_root, output, split):
    raw = inputs.read_bytes()
    source = json.loads(raw)
    if source.get('schema_version') != '1':
        raise ValueError('unsupported conversation input schema')
    mode = source.get('mode', 'conversation')
    if mode not in {'conversation', 'interaction'}:
        raise ValueError('unsupported input mode')
    if mode == 'interaction' and split != 'all':
        raise ValueError('interaction inputs require --split all')
    cases = []
    for case in source['cases']:
        if mode == 'interaction':
            turns = [{'id': turn['id'], 'message': turn['message'],
                      'freshSession': turn.get('freshSession', False)} for turn in case['turns']]
            cases.append({'id': case['id'], 'readOnly': False, 'turns': turns,
                          'insights': case.get('insights', []), 'edges': case.get('edges', [])})
            continue
        if split != 'all' and case['split'] != split:
            continue
        insights = []
        for session in case['sessions']:
            for turn in session['turns']:
                insights.append({
                    'content': (f"[turn_id={turn['id']}; speaker={turn['speaker']}; "
                                f"session_date={session['date_time']}] {turn['text']}"),
                    'category': 'context', 'importance': 3, 'source': session['id'],
                    'created_at': session['date_time'],
                })
        turns = []
        for question in case['questions']:
            slots = json.dumps(question['answer_slots'], ensure_ascii=False)
            message = (
                question['text'] + '\n\nUse the persistent conversation memory as evidence. '
                'You may make several focused mnemon recall/search/show/related calls. '
                'Do not guess missing facts or treat an assistant suggestion as a confirmed user decision. '
                'Return only JSON with keys slots, evidence_turn_ids, abstain. '
                f'The slots object must have these keys: {slots}. '
                'Use null for a requested value that memory cannot establish. '
                'evidence_turn_ids must name the supporting turn_id labels found inside retrieved memories. '
                'Set abstain=true if the requested answer cannot be established. '
                'This is a read-only question; do not change the store.'
            )
            turns.append({'id': question['id'], 'message': message, 'freshSession': True})
        cases.append({'id': case['id'], 'readOnly': True, 'insights': insights, 'turns': turns})
    if not cases or len(cases) > 64:
        raise ValueError('select between 1 and 64 cases per run')
    question_ids = [turn['id'] for case in cases for turn in case['turns']]
    if len({case['id'] for case in cases}) != len(cases) or len(set(question_ids)) != len(question_ids):
        raise ValueError('case and question IDs must be unique')
    if any(not 1 <= len(case['turns']) <= 32 for case in cases):
        raise ValueError('each case must contain between 1 and 32 questions')
    if any(not isinstance(case['id'], str) or not case['id'] for case in cases):
        raise ValueError('case IDs must be nonempty strings')
    for case in cases:
        for turn in case['turns']:
            if (not isinstance(turn['id'], str) or not turn['id']
                    or not isinstance(turn['message'], str) or not 1 <= len(turn['message']) <= 200000
                    or type(turn.get('freshSession', False)) is not bool):
                raise ValueError('turns require a nonempty ID, 1–200000 character message, and boolean freshSession')
    return {'authorizeLive': False, 'piRoot': str(pi_root.resolve()),
            'binary': str(binary.resolve()), 'output': str(output.resolve()),
            'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest() if binary.is_file() else None,
            'input_sha256': hashlib.sha256(raw).hexdigest(), 'mode': mode, 'cases': cases}


def extract_answers(result, expected_cases=None, parse_responses=True):
    answers, errors = {}, []
    seen_cases, seen_questions = set(), set()
    expected = ({case['id']: {turn['id'] for turn in case['turns']} for case in expected_cases}
                if expected_cases is not None else None)
    if result.get('error'):
        errors.append({'kind': 'runner_error'})
    if result.get('completed') is False or result.get('credential_cleanup_error'):
        errors.append({'kind': 'incomplete_runner'})
    for case in result.get('cases', []):
        case_id = case['id']
        if case_id in seen_cases or (expected is not None and case_id not in expected):
            errors.append({'case': case_id, 'kind': 'unexpected_or_duplicate_case'})
        seen_cases.add(case_id)
        if case.get('error') or case.get('infrastructure_error') or not case.get('completed'):
            errors.append({'case': case_id, 'kind': 'incomplete_or_provider_error'})
        case_questions = set()
        for turn in case.get('turns', []):
            question_id = turn['id']
            if question_id in seen_questions or (expected is not None and question_id not in expected.get(case_id, set())):
                errors.append({'question': question_id, 'kind': 'unexpected_or_duplicate_question'})
                continue
            seen_questions.add(question_id)
            case_questions.add(question_id)
            if turn.get('error') or turn.get('timedOut') or turn.get('stopReason') != 'stop':
                errors.append({'question': question_id, 'kind': turn.get('failure_kind', 'generation_error')})
                continue
            if not parse_responses:
                answers[question_id] = turn['response']
                continue
            try:
                answers[question_id] = json.loads(turn['response'])
            except (json.JSONDecodeError, TypeError):
                # Malformed model output is a scored answer failure, not a provider outage.
                answers[question_id] = {'invalid_model_output': True}
        if expected is not None and case_questions != expected.get(case_id, set()):
            errors.append({'case': case_id, 'kind': 'missing_questions'})
    if expected is not None and seen_cases != set(expected):
        errors.append({'kind': 'missing_cases'})
    return answers, errors


def run_with_deadline(command, key, env, timeout):
    """Reap the runner and its CLI children on timeout, including stuck providers."""
    with subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, text=True, env=env,
                          start_new_session=os.name == 'posix') as child:
        try:
            stdout, stderr = child.communicate(key, timeout=timeout)
        except (subprocess.TimeoutExpired, KeyboardInterrupt):
            if os.name == 'posix':
                try:
                    os.killpg(child.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
            else:
                child.terminate()
            try:
                child.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                if os.name == 'posix':
                    try:
                        os.killpg(child.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                else:
                    child.kill()
                child.communicate()
            if os.name == 'posix':
                # A descendant may close its pipes and outlive the Node parent.
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            raise
        return subprocess.CompletedProcess(command, child.returncode, stdout, stderr)


def write_incomplete(output, expected, answers, errors, reason):
    (output / 'incomplete.json').write_text(json.dumps({
        'expected_questions': expected, 'completed_answers': len(answers), 'errors': errors,
        'accuracy': None, 'reason': reason}, indent=2) + '\n')


def cleanup_scratch(output, keep_scratch):
    scratch = output / 'scratch'
    if not keep_scratch and scratch.is_dir() and not scratch.is_symlink():
        shutil.rmtree(scratch)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', required=True, type=Path)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--pi-root', type=Path, default=Path(__file__).resolve().parent)
    parser.add_argument('--split', choices=['all', 'dev', 'holdout', 'external'], default='all')
    parser.add_argument('--live', action='store_true', help='authorize paid provider requests')
    parser.add_argument('--prepare-only', action='store_true', help='write model inputs without requesting a key or a model')
    parser.add_argument('--provider-base-url', default=DEFAULT_PROVIDER_BASE_URL,
                        help='explicit non-sensitive DeepSeek endpoint; model and provider stay fixed')
    parser.add_argument('--prompt-timeout-seconds', type=int, default=90)
    parser.add_argument('--run-timeout-seconds', type=int, help='overall process deadline; default allows setup and each prompt')
    parser.add_argument('--keep-scratch', action='store_true', help='retain private databases for independent read-only checks')
    args = parser.parse_args()
    if not args.live and not args.prepare_only:
        parser.error('--live is required for paid evaluation; use --prepare-only otherwise')
    if not 1 <= args.prompt_timeout_seconds <= 300 or (args.run_timeout_seconds is not None and args.run_timeout_seconds <= 0):
        parser.error('prompt timeout must be 1–300 seconds and the run timeout must be positive')
    try:
        endpoint = provider_base_url(args.provider_base_url)
        config = prepare(args.inputs, args.binary, args.pi_root, args.output, args.split)
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.error(str(error))
    config.update(authorizeLive=args.live and not args.prepare_only, providerBaseUrl=endpoint,
                  promptTimeoutMs=args.prompt_timeout_seconds * 1000, keepScratch=args.keep_scratch)
    if not args.prepare_only:
        if not args.binary.is_file() or not os.access(args.binary, os.X_OK):
            parser.error('binary must be an existing executable')
        package = args.pi_root / 'node_modules/@earendil-works/pi-coding-agent/package.json'
        if not package.exists() or json.loads(package.read_text()).get('version') != '0.83.0':
            parser.error('install the pinned runtime with npm ci --prefix test/memory/pi')
    expected = sum(len(c['turns']) for c in config['cases'])
    timeout = args.run_timeout_seconds or (180 * len(config['cases']) + (args.prompt_timeout_seconds + 10) * expected + 30)
    config['supervisorTimeoutMs'] = timeout * 1000
    if args.output.exists() and any(args.output.iterdir()):
        parser.error('output directory must be new or empty; preserve prior observations')
    args.output.mkdir(parents=True, exist_ok=True)
    config_path = args.output / 'config.json'
    config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + '\n')
    if args.prepare_only:
        print(json.dumps({'prepared_cases': len(config['cases']), 'config': str(config_path)}))
        return 0
    key = os.environ.pop('DEEPSEEK_API_KEY', '') or getpass.getpass('DeepSeek API key (hidden): ')
    if not key:
        parser.error('a DeepSeek API key is required')
    env = {name: value for name, value in os.environ.items()
           if not any(part in name.lower() for part in ['api_key', 'apikey', 'token', 'secret', 'password', 'credential'])
           and name not in {'NODE_OPTIONS', 'NODE_PATH', 'PYTHONPATH', 'PYTHONSTARTUP', 'BASH_ENV', 'ENV'}}
    command = ['node', str(Path(__file__).with_name('run.mjs')), str(config_path)]
    try:
        run = run_with_deadline(command, key, env, timeout)
    except subprocess.TimeoutExpired:
        cleanup_scratch(args.output, args.keep_scratch)
        write_incomplete(args.output, expected, {}, [{'kind': 'runner_deadline'}],
                         'Incomplete run; deadline failures are not memory scores.')
        print('Provider evaluation exceeded its deadline; no accuracy result is reported.', file=sys.stderr)
        return 2
    except (OSError, KeyboardInterrupt):
        cleanup_scratch(args.output, args.keep_scratch)
        write_incomplete(args.output, expected, {}, [{'kind': 'runner_interrupted_or_unavailable'}],
                         'Incomplete run; no accuracy result is reported.')
        return 2
    for value in [run.stdout, run.stderr]:
        if value:
            print(value.replace(key, '[REDACTED]'), end='')
    key = None
    result_path = args.output / 'results.json'
    if not result_path.exists():
        write_incomplete(args.output, expected, {}, [{'kind': 'missing_results'}], 'Runner did not produce results.')
        return 2
    try:
        result = json.loads(result_path.read_text())
        answers, errors = extract_answers(result, config['cases'], config['mode'] != 'interaction')
    except (ValueError, TypeError, KeyError, AttributeError):
        write_incomplete(args.output, expected, {}, [{'kind': 'invalid_results'}], 'Runner produced invalid results.')
        return 2
    if errors or len(answers) != expected or run.returncode:
        if run.returncode:
            errors.append({'kind': 'runner_exit', 'exit_code': run.returncode})
        write_incomplete(args.output, expected, answers, errors,
                         'Incomplete run; provider failures are not memory scores.')
        return 2
    if config['mode'] == 'interaction':
        return 0
    (args.output / 'answers.json').write_text(json.dumps(answers, ensure_ascii=False, indent=2) + '\n')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
