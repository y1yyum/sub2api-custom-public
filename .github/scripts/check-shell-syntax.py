#!/usr/bin/env python3
"""检查公开仓库实际纳管的全部 Shell 文件，不引用开发仓库专用文件。"""
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
paths = subprocess.check_output(['git', '-C', str(ROOT), 'ls-files', '-z']).decode().split('\0')
scripts = sorted(path for path in paths if path.endswith('.sh'))
if not scripts:
    raise SystemExit('no tracked shell scripts found')
for path in scripts:
    subprocess.run(['bash', '-n', path], cwd=ROOT, check=True)
print(f'Shell syntax passed: {len(scripts)} tracked files')
