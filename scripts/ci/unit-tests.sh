#!/usr/bin/env bash
set -euo pipefail

# E2E suites have their own release job. Every other Go package is included;
# do not maintain a production-package denylist or use -short here.
report_dir="${UNIT_REPORT_DIR:-artifacts/unit-tests}"
mkdir -p "$report_dir"
go list ./... > "$report_dir/all-packages.txt"
python3 - "$report_dir" <<'PY'
import pathlib, sys
root = pathlib.Path(sys.argv[1])
packages = root.joinpath('all-packages.txt').read_text().splitlines()
unit, e2e = [], []
for package in packages:
    parts = package.split('/')
    (e2e if 'e2e' in parts or 'e2e-polybft' in parts else unit).append(package)
if not unit:
    raise SystemExit('No Go packages discovered')
root.joinpath('packages.txt').write_text('\n'.join(unit) + '\n')
root.joinpath('e2e-packages.txt').write_text('\n'.join(e2e) + '\n')
print(f'Unit package coverage: {len(unit)} packages; {len(e2e)} E2E packages run separately')
PY
mapfile -t unit_packages < "$report_dir/packages.txt"
test_exit=0
go test -count=1 -timeout=20m -json "${unit_packages[@]}" | tee "$report_dir/events.jsonl" || test_exit=$?
python3 - "$report_dir" <<'PY'
import collections, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
counts = collections.Counter()
skipped = []
for line in root.joinpath('events.jsonl').read_text().splitlines():
    event = json.loads(line)
    if event.get('Test') and event['Action'] in ('pass', 'fail', 'skip'):
        counts[event['Action']] += 1
        if event['Action'] == 'skip':
            skipped.append(f"{event['Package']} {event['Test']}")
summary = {'passed': counts['pass'], 'failed': counts['fail'],
           'skipped': counts['skip'], 'skipped_tests': skipped}
root.joinpath('summary.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary, indent=2))
PY
exit "$test_exit"
