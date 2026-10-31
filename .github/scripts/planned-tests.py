"""在既有隔离边界内执行固定范围；完整单元测试使用有界并行分片。"""
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import re
import runpy
import subprocess
import threading

contract = runpy.run_path(str(Path(__file__).with_name('test-plan.py')))


def execute_suite(api, source, doc, env, expected_digest, suite, execute, fixture):
    plan = contract['validate'](doc['ci_plan'], suite, doc['source_commit'])
    compiled, compiled_digest = api.verify_compiled(source, doc, expected_digest, suite)
    lock = threading.Lock()
    receipt = {'isolation_preflight': 'passed', 'suite': suite, 'mode': plan['mode'],
               'source_commit': doc['source_commit'], 'manifest_sha256': expected_digest,
               'source_receipt_sha256': doc['source_receipt_sha256'], 'files': doc['files'],
               'environment_keys': sorted(env), 'test_execution': 'precompiled race binaries; source-bound test plan',
               'compile_receipt_sha256': compiled_digest, 'compiled_package_count': compiled['package_count'],
               'synthetic_service_child_environment': api.SYNTHETIC_SERVICE_ENV,
               'ci_plan': plan, 'planned_package_audits': [], 'test_commands': [],
               'no_test_file_packages': [r['package'] for r in compiled['packages'] if r['binary'] is None],
               'test_exit_code': None, 'fixture_receipt': fixture, 'package_results': [],
               'child_process_resources': []}

    def emit(value):
        with lock:
            print(json.dumps(value, sort_keys=True), flush=True)

    emit({'record_type': 'isolation_receipt', **receipt})

    def observed(label, record, command, capture=False):
        child = Path('/tmp/planned-children', api.digest(label.encode()))
        child.mkdir(mode=0o700, parents=True, exist_ok=False)
        child_env = api.package_environment(record, env)
        for key, name in [('HOME', 'home'), ('GOCACHE', 'gocache'), ('GOTMPDIR', 'gotmp'), ('TMPDIR', 'tmp')]:
            path = child / name
            path.mkdir(mode=0o700)
            child_env[key] = str(path)
        before = api.child_resource_snapshot()
        emit({'record_type': 'child_resource_start', 'label': label, 'before': before})
        result = None
        try:
            if capture:
                result = execute(command, cwd=source / record['directory'], env=child_env,
                                 capture_output=True, text=True, check=False, timeout=1200)
            else:
                output = child / 'events.jsonl'
                with output.open('w') as stream:
                    result = execute(command, cwd=source / record['directory'], env=child_env,
                                     stdout=stream, stderr=subprocess.STDOUT, check=False, timeout=1200)
                with lock, output.open() as stream:
                    for line in stream:
                        print(line, end='')
                    print('', flush=True)
                output.unlink()
            return result
        finally:
            observation = {'label': label, 'before': before, 'after': api.child_resource_snapshot(),
                           'raw_exit_code': getattr(result, 'returncode', None),
                           'usage_scope': 'whole-container counters may overlap parallel children',
                           'exit_status_scope': 'metadata binary or go tool test2json; underlying test signal unknown'}
            observation['caches'] = api.cleanup_test_caches((child / 'gocache', child / 'gotmp'))
            observation['cache_cleanup'] = 'completed'
            receipt['child_process_resources'].append(observation)
            emit({'record_type': 'child_resource_end', **observation})

    plans = []
    for record in compiled['packages']:
        if record['binary'] is None:
            receipt['package_results'].append({'package': record['package'], 'status': 'no_test_files', 'compiled': True})
            continue
        command = [str(api.COMPILED_ROOT / record['binary']), '-test.list=.', '-test.timeout=20m']
        listed = observed(record['package'] + ':registry', record, command, capture=True)
        api.require(listed.returncode == 0, 'planned package registry failed')
        registry, benchmarks = api.parse_test_registry(listed.stdout)
        selected, parts = contract['memberships'](plan, record['package'], registry)
        audit = {'package': record['package'], 'binary_sha256': record['binary_sha256'],
                 'discovery_exit_code': listed.returncode, 'discovery_command': command,
                 'registry': registry, 'benchmarks_not_run_by_default': benchmarks, 'selected': selected, 'partitions': []}
        for part in parts:
            if plan['mode'] == 'full' and part['name'] == 'main':
                excluded = sorted(set(registry) - set(part['tests']))
                regex = api.fixed_exclusion_regex(excluded) if excluded else '^.*$'
            else:
                regex = '^(' + '|'.join(re.escape(n) for n in part['tests']) + ')$'
            api.require(len(regex) <= 64 * 1024, 'planned regex exceeds argument bound')
            command = [str(api.COMPILED_ROOT / record['binary']), '-test.list=' + regex, '-test.timeout=20m']
            checked = observed(record['package'] + ':verify:' + part['name'], record, command, capture=True)
            api.require(checked.returncode == 0, 'planned Go regular expression discovery failed')
            names, _ = api.parse_test_registry(checked.stdout)
            api.require(names == part['tests'], 'planned Go regular expression coverage mismatch')
            part = {**part, 'regex': regex, 'go_regex_verified': True}
            audit['partitions'].append(part)
            plans.append((record, part['name'], [*api.binary_command(record), '-test.run=' + regex]))
        receipt['planned_package_audits'].append(audit)
        emit({'record_type': 'planned_package_audit', **audit})
    receipt['test_commands'] = [command for _, _, command in plans]

    def run_one(item):
        record, name, command = item
        try:
            completed = observed(record['package'] + ':test:' + name, record, command)
            raw = completed.returncode
            code = raw if raw >= 0 else 128 - raw
        except subprocess.TimeoutExpired:
            raw = code = 124
        result = {'package': record['package'], 'partition': name,
                  'binary_sha256': record['binary_sha256'], 'raw_exit_code': raw, 'exit_code': code}
        emit({'record_type': 'planned_partition_exit', **result})
        return result

    # 固定调度在首条测试结果前完成；失败不增加重试，也不改变其他分片。
    workers = 2 if plan['mode'] == 'full' and suite == 'unit' else 1
    with ThreadPoolExecutor(max_workers=workers) as pool:
        futures = [pool.submit(run_one, item) for item in plans]
        receipt['package_results'].extend(future.result() for future in futures)
    receipt['test_exit_code'] = int(any(r.get('exit_code', 0) != 0 for r in receipt['package_results']))
    return receipt
