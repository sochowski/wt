#!/usr/bin/env python3
"""Survey what was running before the Rex server last died.

Rex keeps no session state on disk, so this rebuilds it from two places:
the old server's journal (sessions alive at the crash, and the blocks they
were still using) and Claude Code transcripts (which conversation ran where,
and whether it stopped mid-turn or waiting on the human).

Usage: python3 -I survey.py [--until ISO] [--window-hours N] [--since-hours N]
  --until         crash time, UTC; default: the current server's started_at
  --window-hours  blocks touched this long before the crash count as open (2)
  --since-hours   transcripts written this long before the crash count (30)
"""
import argparse, datetime as dt, glob, json, os, re, subprocess

ap = argparse.ArgumentParser()
ap.add_argument('--until')
ap.add_argument('--window-hours', type=float, default=2)
ap.add_argument('--since-hours', type=float, default=30)
a = ap.parse_args()

def utc(s):
    return dt.datetime.fromisoformat(s.replace('Z', '+00:00')).astimezone(dt.timezone.utc)

if a.until:
    until = utc(a.until)
else:
    w = json.loads(subprocess.run(['rex', 'whoami', '--json'], capture_output=True, text=True).stdout)
    until = utc(w['started_at'][:26] + 'Z')
since = until - dt.timedelta(hours=a.since_hours)
win = until - dt.timedelta(hours=a.window_hours)

# --- journal: sessions created and not destroyed, and blocks seen late ---
log = subprocess.run(
    ['journalctl', '--user', '-u', 'rex', '--no-pager', '-o', 'cat',
     '--since', (until - dt.timedelta(days=7)).strftime('%Y-%m-%d %H:%M:%S UTC'),
     '--until', until.strftime('%Y-%m-%d %H:%M:%S UTC')],
    capture_output=True, text=True).stdout.splitlines()
TS = re.compile(r'^(\d{4}/\d\d/\d\d \d\d:\d\d:\d\d)')
alive, blocks = {}, {}
runs = []  # (alive, blocks) per server run
for line in log:
    if line.startswith('Started rex.service'):  # a new server: nothing before it survived
        if alive: runs.append((alive, blocks))
        alive, blocks = {}, {}
        continue
    m = re.search(r'session (created|destroyed) .*session_id=(session:\S+) label=("[^"]*"|\S+)', line)
    if m:
        if m[1] == 'created': alive[m[2]] = m[3].strip('"')
        else: alive.pop(m[2], None)
        continue
    m = re.search(r'session_id=(session:\S+) block_id=(block:\S+)', line)
    t = TS.match(line)
    if m and t:
        ts = dt.datetime.strptime(t[1], '%Y/%m/%d %H:%M:%S').replace(tzinfo=dt.timezone.utc)
        if ts >= win:
            blocks.setdefault(m[1], {}).setdefault(m[2], 0)
            blocks[m[1]][m[2]] += 1

# the crashed server is the last run that had sessions; failed restarts log none
if not alive and runs: alive, blocks = runs[-1]

# --- transcripts: one row per conversation written in the window ---
proj = os.path.expanduser('~/.claude/projects')
convs = []
for f in glob.glob(os.path.join(proj, '*', '*.jsonl')):
    mt = dt.datetime.fromtimestamp(os.path.getmtime(f), dt.timezone.utc)
    if mt < since: continue
    cwd = name = None; last = last_text = None
    with open(f, errors='replace') as fh: text = fh.read()
    for line in text.splitlines():
        try: d = json.loads(line)
        except ValueError: continue
        cwd = cwd or d.get('cwd')
        if d.get('type') in ('custom-title', 'agent-name'):
            name = d.get('customTitle') or d.get('agentName')
        if d.get('type') not in ('user', 'assistant'): continue
        if d.get('timestamp') and utc(d['timestamp']) > until: continue
        if d.get('isCompactSummary'): continue
        last = d
        c = d['message'].get('content')
        if d['type'] == 'assistant' and isinstance(c, list):
            t = ' '.join(x.get('text', '') for x in c if x.get('type') == 'text').strip()
            if t: last_text = t
    if not last: continue
    c = last['message'].get('content')
    kinds = [x.get('type') for x in c] if isinstance(c, list) else ['text']
    state = 'MID-TURN' if last['type'] == 'user' or 'tool_use' in kinds else 'idle'
    hits = {b for s in blocks.values() for b in s if b[6:] in text or b[-6:] in text}
    convs.append(dict(id=os.path.basename(f)[:-6], name=name, cwd=cwd, state=state,
                      at=last.get('timestamp', '')[:16], blocks=hits, tail=(last_text or '')[-400:]))

print(f'# crash at {until:%Y-%m-%d %H:%M} UTC\n')
print('## Sessions alive at the crash (label is the one at creation; it may have been renamed)')
for s, lbl in alive.items():
    bs = blocks.get(s, {})
    print(f'- {s}  {lbl}  ({len(bs)} blocks active in the last {a.window_hours:g}h)')
    for b in bs:
        who = [c['name'] or c['id'][:8] for c in convs if b in c['blocks']]
        print(f'    {b}  mentioned by: {", ".join(who) or "-"}')
print('\n## Conversations (mentions of a block are hints, not proof of where it ran)')
for c in sorted(convs, key=lambda c: c['at']):
    print(f"- {c['at']}  {c['state']:8}  {c['id']}  name={c['name']}  cwd={c['cwd']}")
print('\n## Last thing each idle conversation said (look for a question to the human)')
for c in sorted(convs, key=lambda c: c['at']):
    if c['state'] == 'idle':
        print(f"### {c['name'] or c['id'][:8]}  ({c['cwd']})\n{c['tail']}\n")
