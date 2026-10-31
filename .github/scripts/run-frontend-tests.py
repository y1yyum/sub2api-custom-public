#!/usr/bin/env python3
"""运行计划指定的前端回归；完整通道不传测试筛选参数。"""
import argparse
import json
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', type=Path, required=True)
    parser.add_argument('--root', type=Path, required=True)
    args = parser.parse_args()
    plan = json.loads(args.plan.read_text())
    command = ['pnpm', 'exec', 'vitest', 'run', '--maxWorkers=2', '--minWorkers=1']
    if plan['mode'] == 'fast':
        paths = plan['frontend_tests']
        if not paths:
            raise SystemExit('frontend fast lane has no test selection')
        for name in paths:
            path = Path(name)
            if path.is_absolute() or '..' in path.parts or not (args.root / path).is_file():
                raise SystemExit('invalid frontend test path')
        command += paths
    print(json.dumps({'mode': plan['mode'], 'selected_files': len(plan['frontend_tests'])}))
    raise SystemExit(subprocess.run(command, cwd=args.root).returncode)


if __name__ == '__main__':
    main()
