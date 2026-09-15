"""Offline runner boundaries; no model accuracy is measured here."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import threading
import unittest

import run_live


HERE = Path(__file__).resolve().parent


class RunnerHelpers(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='mnemon-runner-test-')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def node(self, code, value=None):
        script = f'import * as runner from {json.dumps((HERE / "run.mjs").as_uri())};\n' + code
        return subprocess.run(['node', '--input-type=module', '-e', script], input=json.dumps(value),
                              text=True, capture_output=True, timeout=15, check=True).stdout

    def inputs(self, source):
        inputs = self.root / 'inputs.json'
        inputs.write_text(json.dumps(source))
        return inputs

    def interaction(self):
        return {'schema_version': '1', 'mode': 'interaction', 'oracle': 'DO_NOT_SEND_ORACLE',
                'cases': [{'id': 'native', 'turns': [{'id': 'remember', 'message': 'Remember my preference.'}]}]}

    def test_live_authorization_precedes_input_reads_and_output_creation(self):
        output = self.root / 'output'
        run = subprocess.run([sys.executable, str(HERE / 'run_live.py'), '--inputs', str(self.root / 'missing.json'),
                              '--binary', '/unused', '--output', str(output)], text=True, capture_output=True)
        self.assertEqual(run.returncode, 2)
        self.assertIn('--live is required', run.stderr)
        self.assertFalse(output.exists())

    def test_interaction_preserves_native_message_and_cannot_authorize_live(self):
        source = self.interaction()
        inputs = self.inputs(source)
        config = run_live.prepare(inputs, Path('/binary'), Path('/pi'), self.root / 'out', 'all')
        self.assertFalse(config['authorizeLive'])
        self.assertFalse(config['cases'][0]['readOnly'])
        self.assertEqual(config['cases'][0]['turns'][0]['message'], 'Remember my preference.')
        self.assertNotIn('DO_NOT_SEND_ORACLE', json.dumps(config))
        with self.assertRaises(ValueError):
            run_live.prepare(inputs, Path('/binary'), Path('/pi'), self.root / 'out', 'dev')
        source['cases'][0]['turns'] *= 33
        with self.assertRaises(ValueError):
            run_live.prepare(self.inputs(source), Path('/binary'), Path('/pi'), self.root / 'out', 'all')

    def test_prompt_deadline_bounds_agree_across_wrapper_and_runner(self):
        inputs = self.inputs(self.interaction())
        for seconds in [1, 90, 900, 0, 901]:
            with self.subTest(seconds=seconds):
                output = self.root / f'prepared-{seconds}'
                run = subprocess.run([sys.executable, str(HERE / 'run_live.py'), '--prepare-only',
                                      '--inputs', str(inputs), '--binary', '/unused', '--output', str(output),
                                      '--prompt-timeout-seconds', str(seconds)], text=True, capture_output=True)
                expected = 1 <= seconds <= 900
                self.assertEqual(run.returncode == 0, expected, run.stderr)
                config = run_live.prepare(inputs, Path('/unused'), Path('/pi'), output, 'all')
                config.update(authorizeLive=True, promptTimeoutMs=seconds * 1000)
                code = f"""
try {{ runner.validateConfig({json.dumps(config)}); console.log('accepted'); }}
catch {{ console.log('rejected'); }}
"""
                self.assertEqual(self.node(code).strip(), 'accepted' if expected else 'rejected')
                if expected:
                    prepared = json.loads((output / 'config.json').read_text())
                    self.assertEqual(prepared['promptTimeoutMs'], seconds * 1000)
                    self.assertFalse(prepared['authorizeLive'])
                else:
                    self.assertFalse(output.exists())

    def test_conversation_prompt_requires_raw_json_without_changing_scoring(self):
        source = {'schema_version': '1', 'oracle': 'DO_NOT_SEND_ORACLE', 'cases': [{
            'id': 'qa', 'split': 'dev', 'sessions': [],
            'questions': [{'id': 'q', 'text': 'What did I decide?', 'answer_slots': ['decision']}],
        }]}
        config = run_live.prepare(self.inputs(source), Path('/binary'), Path('/pi'), self.root / 'out', 'dev')
        message = config['cases'][0]['turns'][0]['message']
        self.assertIn('single raw JSON object, without Markdown fences or surrounding prose', message)
        self.assertIn('with keys slots, evidence_turn_ids, abstain', message)
        self.assertIn('The slots object must have these keys: ["decision"]', message)
        self.assertIn('Use null for a requested value that memory cannot establish', message)
        self.assertIn('Set abstain=true', message)
        self.assertNotIn('DO_NOT_SEND_ORACLE', json.dumps(config))
        fenced = '```json\n{"slots":{"decision":null},"evidence_turn_ids":[],"abstain":true}\n```'
        report = {'cases': [{'id': 'qa', 'completed': True,
                            'turns': [{'id': 'q', 'stopReason': 'stop', 'response': fenced}]}]}
        answers, errors = run_live.extract_answers(report)
        self.assertFalse(errors)
        self.assertEqual(answers['q'], {'invalid_model_output': True})

    def test_failed_generation_and_mismatched_ids_are_not_answers(self):
        expected = [{'id': 'c', 'turns': [{'id': 'q'}]}]
        report = {'completed': False, 'cases': [{'id': 'c', 'completed': False, 'infrastructure_error': True,
                  'turns': [{'id': 'q', 'stopReason': 'error', 'error': '503', 'response': '{}'}]}]}
        answers, errors = run_live.extract_answers(report, expected)
        self.assertFalse(answers)
        self.assertTrue(errors)
        report = {'cases': [{'id': 'c', 'completed': True,
                  'turns': [{'id': 'wrong', 'stopReason': 'stop', 'response': '{}'}]}]}
        self.assertTrue(run_live.extract_answers(report, expected)[1])

    def test_malformed_answer_is_scored_but_interaction_is_not_json_decoded(self):
        report = {'cases': [{'id': 'c', 'completed': True, 'turns': [{'id': 'q', 'stopReason': 'stop', 'response': 'Saved.'}]}]}
        answers, errors = run_live.extract_answers(report)
        self.assertFalse(errors)
        self.assertEqual(answers['q'], {'invalid_model_output': True})
        self.assertEqual(run_live.extract_answers(report, parse_responses=False)[0]['q'], 'Saved.')

    def test_endpoint_and_fixed_store_guards(self):
        for endpoint in ['https://user:pass@example.com', 'https://example.com?key=x', 'http://example.com']:
            with self.assertRaises(ValueError):
                run_live.provider_base_url(endpoint)
        self.assertEqual(run_live.provider_base_url('http://127.0.0.1:1/'), 'http://127.0.0.1:1')
        result = json.loads(self.node("""
const denied = [];
for (const command of ['mnemon recall x --store=other', 'mnemon recall x --readonly=false', 'mnemon remember x', 'cat /etc/passwd']) {
  try { runner.fixedCommand(command, '/private-memory', true); denied.push(false); } catch { denied.push(true); }
}
console.log(JSON.stringify({denied, fixed: runner.fixedCommand('mnemon recall "project history" --brief', '/private-memory', true),
  redacted: runner.safe({message: 'credential-example', nested: ['credential-example']}, 'credential-example')}));
"""))
        self.assertEqual(result['denied'], [True] * 4)
        self.assertEqual(result['fixed'][:5], ['--data-dir', '/private-memory', '--store', 'default', '--readonly'])
        self.assertNotIn('credential-example', result['redacted'])

    def test_command_interface_accepts_help_and_literals_but_rejects_compound_commands(self):
        accepted = [
            ('mnemon --help', True, ['--help']),
            ('mnemon -h', True, ['-h']),
            ('mnemon help', True, ['help']),
            ('mnemon help remember', True, ['help', 'remember']),
            ('mnemon recall "project history" --brief', True, ['recall', 'project history', '--brief']),
            ('mnemon remember "PostgreSQL; Tuesday 09:00 UTC | x < y & z"', False,
             ['remember', 'PostgreSQL; Tuesday 09:00 UTC | x < y & z']),
            ("mnemon remember 'Line one;\nline two {\"label\":\"a;b\"}'", False,
             ['remember', 'Line one;\nline two {"label":"a;b"}']),
            (r'mnemon remember "He said \"keep; both\"."', False, ['remember', 'He said "keep; both".']),
            (r'mnemon remember A\;B', False, ['remember', 'A;B']),
            ('mnemon remember ";"', False, ['remember', ';']),
        ]
        denied = [
            'cd /tmp && mnemon status',
            'mnemon recall x --limit 5;echo done',
            'mnemon show one;mnemon show two',
            'mnemon show one\nmnemon show two',
            'mnemon show one\r\nmnemon show two',
            'mnemon status|cat',
            'mnemon status&&mnemon status',
            'mnemon status>output',
            'mnemon status &',
            'mnemon recall "unterminated',
        ]
        code = f"""
const accepted = {json.dumps(accepted)}.map(([command, readOnly]) => runner.fixedCommand(command, '/private-memory', readOnly));
const denied = {json.dumps(denied)}.map(command => {{
  try {{ runner.fixedCommand(command, '/private-memory', false); return null; }} catch (error) {{ return error.message; }}
}});
console.log(JSON.stringify({{accepted, denied}}));
"""
        result = json.loads(self.node(code))
        for (command, read_only, args), actual in zip(accepted, result['accepted']):
            with self.subTest(command=command):
                prefix = ['--data-dir', '/private-memory', '--store', 'default'] + (['--readonly'] if read_only else [])
                self.assertEqual(actual, prefix + args)
        for command, message in zip(denied, result['denied']):
            with self.subTest(command=command):
                self.assertIsNotNone(message)
                self.assertIn('Run exactly one mnemon command', message)
                self.assertIn('working directory is already set', message)
                self.assertIn('read tool', message)

    def test_skill_symlink_and_snapshot_symlink_cannot_escape(self):
        skill = self.root / 'skills'
        skill.mkdir()
        outside = self.root / 'outside'
        outside.write_text('private')
        (skill / 'link.md').symlink_to(outside)
        data = self.root / 'memory'
        (data / 'data/default').mkdir(parents=True)
        (data / 'data/default/mnemon.db').symlink_to(outside)
        code = f"""
let denied = 0;
try {{ runner.readableSkillPath({json.dumps(str(self.root))}, 'skills/link.md', [{json.dumps(str(skill))}]); }} catch {{ denied++; }}
try {{ runner.readMemorySnapshot({json.dumps(str(data))}); }} catch {{ denied++; }}
console.log(denied);
"""
        self.assertEqual(self.node(code).strip(), '2')

    def test_independent_sqlite_snapshot_is_bounded_and_excludes_embeddings(self):
        data = self.root / 'memory'
        store = data / 'data/default'
        store.mkdir(parents=True)
        with sqlite3.connect(store / 'mnemon.db') as db:
            self.addCleanup(db.close)
            db.execute('PRAGMA journal_mode=WAL')
            db.execute('CREATE TABLE insights(id,content,category,source,created_at,deleted_at,embedding)')
            db.execute('CREATE TABLE edges(source_id,target_id,edge_type,weight)')
            db.executemany('INSERT INTO insights VALUES(?,?,?,?,?,?,?)',
                           [(str(i), 'fact', 'fact', 'user', '2026-01-01', None, 'DO_NOT_EXPORT') for i in range(257)])
            db.execute("INSERT INTO edges VALUES('1','0','supersedes',1)")
        # Keep the connection open so the committed facts remain in WAL.
        wal = store / 'mnemon.db-wal'
        self.assertGreater(wal.stat().st_size, 0)
        before = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in [store / 'mnemon.db', wal]}
        result = json.loads(self.node(f'console.log(JSON.stringify(runner.readMemorySnapshot({json.dumps(str(data))})));'))
        self.assertEqual(before, {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in [store / 'mnemon.db', wal]})
        self.assertEqual(len(result['insights']), 256)
        self.assertEqual(result['total_insights'], 257)
        self.assertTrue(result['truncated']['insights'])
        self.assertEqual(result['edges'][0]['edge_type'], 'supersedes')
        self.assertNotIn('DO_NOT_EXPORT', json.dumps(result))

    @unittest.skipUnless(os.name == 'posix', 'process-group deadline boundary is POSIX')
    def test_outer_deadline_reaps_process_group(self):
        marker = self.root / 'late-write'
        script = ("import subprocess,time,sys; subprocess.Popen([sys.executable,'-c',"
                  + repr(f"import time,pathlib; time.sleep(1); pathlib.Path({str(marker)!r}).write_text('orphan')")
                  + "]); time.sleep(30)")
        with self.assertRaises(subprocess.TimeoutExpired):
            run_live.run_with_deadline([sys.executable, '-c', script], 'not-a-secret', dict(os.environ), 0.2)
        # An unreaped child would create the marker during this wait.
        subprocess.run([sys.executable, '-c', 'import time; time.sleep(1.1)'], check=True)
        self.assertFalse(marker.exists())

    @unittest.skipUnless(os.environ.get('PI_MEMORY_PACKAGE_DIR') and os.environ.get('MNEMON_BIN'),
                         'set PI_MEMORY_PACKAGE_DIR and MNEMON_BIN for the real SDK failure boundary')
    def test_real_sdk_failure_cleans_scope_and_never_produces_a_score(self):
        package = Path(os.environ['PI_MEMORY_PACKAGE_DIR'])
        output = self.root / 'output'
        poisoned = self.root / '.pi/extensions'
        poisoned.mkdir(parents=True)
        (poisoned / 'untrusted.ts').write_text('throw new Error("UNTRUSTED_EXTENSION_LOADED");')
        requests = []
        class UnavailableProvider(BaseHTTPRequestHandler):
            def do_POST(self):
                requests.append({'path': self.path, 'body': json.loads(self.rfile.read(int(self.headers['Content-Length'])))})
                body = b'{"error":{"message":"offline boundary fixture unavailable","type":"server_error"}}'
                self.send_response(503)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_args):
                pass
        server = ThreadingHTTPServer(('127.0.0.1', 0), UnavailableProvider)
        worker = threading.Thread(target=server.serve_forever)
        worker.start()
        def stop_server():
            server.shutdown()
            server.server_close()
            worker.join(timeout=5)
        self.addCleanup(stop_server)
        endpoint = f'http://127.0.0.1:{server.server_port}/fixture'
        env = dict(os.environ, DEEPSEEK_API_KEY='offline-placeholder-only', MNEMON_STORE='user-global-store')
        run = subprocess.run([sys.executable, str(HERE / 'run_live.py'), '--live', '--inputs', str(self.inputs(self.interaction())),
                              '--binary', env['MNEMON_BIN'], '--pi-root', str(package.parents[2]), '--output', str(output),
                              '--provider-base-url', endpoint, '--prompt-timeout-seconds', '900'],
                             text=True, capture_output=True, env=env, timeout=30)
        self.assertEqual(run.returncode, 2, run.stdout + run.stderr)
        report = json.loads((output / 'results.json').read_text())
        self.assertFalse(report['completed'])
        self.assertEqual(report['model'], 'deepseek-flash')
        self.assertTrue(report['cases'], json.dumps(report))
        self.assertTrue(requests, json.dumps(report))
        self.assertEqual(requests[0]['path'], '/fixture/chat/completions')
        self.assertEqual(requests[0]['body']['model'], 'deepseek-flash')
        bash = next(tool['function'] for tool in requests[0]['body']['tools'] if tool['function']['name'] == 'bash')
        self.assertIn('Run exactly one mnemon command', bash['description'])
        self.assertIn('working directory is already set', bash['description'])
        self.assertIn('this tool does not execute a shell', bash['description'])
        self.assertNotIn('--brief', bash['description'])
        system = next(message['content'] for message in requests[0]['body']['messages'] if message['role'] == 'system')
        self.assertIn('Run exactly one mnemon command', system)
        self.assertNotIn('DO_NOT_SEND_ORACLE', json.dumps(requests))
        self.assertNotIn('UNTRUSTED_EXTENSION_LOADED', json.dumps(requests))
        self.assertEqual(len(report['binary_sha256']), 64)
        self.assertEqual(report['budgets']['prompt_timeout_ms'], 900000)
        row = report['cases'][0]
        request_events = [event for event in row['events'] if event['type'] == 'request']
        self.assertEqual(len(request_events), len(requests))
        self.assertTrue(all(event['turn_id'] == 'remember' for event in request_events))
        self.assertTrue(all(event['turn_id'] == 'remember' for event in row['events'] if event['type'] == 'assistant'))
        self.assertFalse(row['completed'])
        self.assertEqual(row['sessions_created'], row['sessions_disposed'])
        self.assertEqual(row['sessions_created'], 1)
        self.assertEqual(row['extension_errors'], [])
        self.assertEqual(row['loaded_resources']['context_files'], 0)
        self.assertEqual(len(row['loaded_resources']['extensions']), 1)
        self.assertTrue(row['loaded_resources']['extensions'][0].endswith('/.pi/extensions/mnemon.ts'))
        self.assertEqual(len(row['loaded_resources']['skills']), 1)
        self.assertTrue(row['loaded_resources']['skills'][0].endswith('/.pi/skills/mnemon/SKILL.md'))
        self.assertIn('/data/default/', row['final_status']['db_path'])
        self.assertIn('final_memory', row, json.dumps(row))
        self.assertEqual(row['final_memory']['total_insights'], 0)
        self.assertFalse((output / 'scratch').exists())
        self.assertFalse((output / 'answers.json').exists())
        self.assertIsNone(json.loads((output / 'incomplete.json').read_text())['accuracy'])
        self.assertNotIn('offline-placeholder-only', run.stdout + run.stderr + (output / 'results.json').read_text())


if __name__ == '__main__':
    unittest.main()
