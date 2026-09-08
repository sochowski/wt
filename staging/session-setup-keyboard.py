#!/usr/bin/env python3
"""Keyboard driver for test-session-setup.sh's private tmux server only."""
import fcntl
import json
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time
from pathlib import Path

artifacts = Path(sys.argv[1])
assert str(artifacts / "sandbox") in os.environ["WT_STATUS_DIR"]
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 45, 150, 0, 0))
client = subprocess.Popen(["tmux", "attach-session", "-t", "sentinel"], stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
os.close(slave)
log = open(artifacts / "keyboard.raw.log", "wb")
screen = b""
vim = os.environ.get("WT_FZF_VIM") in ("1", "true")

def pump(seconds=0.15):
    global screen
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        if select.select([master], [], [], 0.05)[0]:
            data = os.read(master, 65536)
            screen += data
            log.write(data)
            log.flush()

def keys(data):
    global screen
    pump()
    screen = b""
    os.write(master, data if isinstance(data, bytes) else data.encode())
    pump(0.25)

def wait_text(text, timeout=12):
    until = time.monotonic() + timeout
    while time.monotonic() < until:
        if text.encode() in screen:
            return
        pump()
    raise AssertionError(f"popup did not show {text!r}; see keyboard.raw.log")

def wt(*args):
    return json.loads(subprocess.check_output(["wt", *args], text=True))

def wait_root(name, repos):
    until = time.monotonic() + 20
    while time.monotonic() < until:
        pump()
        roots = wt("roots", name)
        if roots and len(roots[0]["checkouts"]) == repos and roots[0]["agents"][0]["native_id"]:
            return roots[0]
    raise AssertionError(f"root {name} not ready with {repos} repositories")

def prefix(key):
    keys(b"\x02" + key.encode())

def search(text):
    keys(("/" if vim else "") + "\x15" + text)
    if vim:
        keys(b"\x1b")
        wait_text("NORMAL")

def cancel():
    keys("q" if vim else b"\x1b")

try:
    pump(0.5)
    # Actual generated prefix+n binding, not direct invocation or an fzf stub.
    prefix("n")
    wait_text("Name this task")
    keys(b"\x15keyboard-task\r")
    wait_text("Repositories")
    if vim:
        wait_text("NORMAL")
        # h/l are neither cancel nor accept, Ctrl-D cannot delete.
        keys("hl")
        keys(b"\x04")
    search("'alpha")
    keys(" " if vim else b"\t")
    search("'beta")
    keys(b"\t")
    # Manual entry returns with exactly the same two live marks.
    keys(b"\x0f")
    wait_text("Full working repository path")
    keys(str(artifacts / "sandbox/repos/beta") + "\r")
    wait_text("Repositories")
    keys(b"\r")
    wait_text("Review")
    # Review can return to the picker without losing either live mark.
    keys("jj" if vim else b"\x1b[B\x1b[B")
    keys(b"\r")
    wait_text("Repositories")
    keys(b"\r")
    wait_text("Review")
    if vim:
        keys("jk")
    # Before Create neither root nor session branch exists.
    assert wt("roots", "keyboard-task") == []
    for repo in ("alpha", "beta"):
        result = subprocess.run(["git", "-C", str(artifacts / "sandbox/repos" / repo), "show-ref", "--verify", "refs/heads/keyboard-task"], capture_output=True)
        assert result.returncode != 0
    keys(b"\r")
    root = wait_root("keyboard-task", 2)
    original = root["agents"][0]
    root_id = root["id"]
    assert root["cwd"] == os.environ["WT_BASE_DIR"] + "/.sessions/keyboard-task"
    assert original["cwd"] == root["cwd"]
    assert Path(root["workspace"]["scratch"]).is_dir()
    for checkout in root["checkouts"]:
        assert (Path(root["cwd"]) / "repos" / checkout["alias"]).resolve() == Path(checkout["path"])
        assert checkout["path"] in (Path(root["cwd"]) / "WORKSPACE.md").read_text()
        assert Path(checkout["path"]).name == "keyboard-task"
        branch = subprocess.check_output(["git", "-C", checkout["path"], "branch", "--show-current"], text=True).strip()
        assert branch == "keyboard-task"
    # Add repositories uses the same flow; existing native conversation survives.
    pump(0.7)
    prefix("R")
    wait_text("Add repositories")
    keys(b"\r")
    wait_text("Repositories")
    keys(b"\x0f")
    wait_text("Full working repository path")
    keys(str(artifacts / "sandbox/manual : ' gamma") + "\r")
    wait_text("Repositories")
    keys(b"\r")
    wait_text("Review")
    keys(b"\r")
    root = wait_root("keyboard-task", 3)
    assert root["id"] == root_id
    assert root["agents"][0]["id"] == original["id"]
    assert root["agents"][0]["native_id"] == original["native_id"]
    # Cancellation is not repo-free creation, including after selection/review.
    pump(0.7)
    prefix("n")
    wait_text("Name this task")
    keys(b"\x15cancelled-task\r")
    wait_text("Repositories")
    # Two no-jump toggles leave EXACT zero, never the highlighted repo.
    search("'alpha")
    keys(" " if vim else b"\t")
    keys(b"\t\r")
    wait_text("No repositories")
    cancel()
    pump(0.5)
    assert wt("roots", "cancelled-task") == []
    if vim:
        prefix("n")
        wait_text("Name this task")
        keys(b"\x15text-cancelled")
        keys(b"\x1b")
        wait_text("NORMAL")
        keys("q")
        pump(0.5)
        assert wt("roots", "text-cancelled") == []
    # Repo-free setup remains a normal agent session.
    prefix("n")
    wait_text("Name this task")
    keys(b"\x15repo-free-task\r")
    wait_text("Repositories")
    search("no-matching-repository")
    keys(b"\x18")
    wait_text("Review")
    keys(b"\r")
    wait_root("repo-free-task", 0)
    # Label does not rename runtime; prefix+s searches names/labels ONLY.
    subprocess.check_call(["wt", "label", "keyboard-task", "Billing migration"])
    # Restore the spy after managed-root startup replaced WT_STATE with its
    # actual executable. Prove the initial names call before asserting silence.
    subprocess.check_call(["tmux", "set-environment", "-t", "=repo-free-task", "WT_STATE", os.environ["WT_STATE"]])
    call_log = Path(os.environ["WT_TEST_STATE_CALLS"])
    calls_initial = call_log.read_text()
    pump(0.7)
    prefix("s")
    wait_text("Select session")
    for _ in range(40):
        calls_before = call_log.read_text()
        if "worktree projection names" in calls_before[len(calls_initial):]:
            break
        pump()
    else:
        raise AssertionError("picker initial name-list call bypassed tracing")
    search("gamma")
    keys(b"\x0c")
    wait_text("0/")
    search(root_id)
    keys(b"\x0c")
    wait_text("0/")
    search("working")
    keys(b"\x0c")
    wait_text("0/")
    search("Billing migration")
    wait_text("Billing migration")
    if vim:
        # Query remains intact across Esc and Ctrl-D is safe, not deletion.
        keys(b"\x04")
    assert Path(os.environ["WT_TEST_STATE_CALLS"]).read_text() == calls_before, "filter/navigation spawned WT inspection"
    keys(b"\r")
    pump(0.7)
    current = subprocess.check_output(["tmux", "list-clients", "-F", "#{session_name}"], text=True).strip()
    assert current == "keyboard-task", current
    if not vim:
        # Delete under a filter, then clear it: other local candidates survive.
        subprocess.check_call(["wt", "new", "filter-delete-victim", "--offline"], stdout=subprocess.DEVNULL)
        prefix("s")
        wait_text("Select session")
        search("filter-delete-victim")
        wait_text("filter-delete-victim")
        keys(b"\x04")
        for _ in range(40):
            pump()
            if not wt("roots", "filter-delete-victim"):
                break
        else:
            raise AssertionError("fixture was not deleted")
        keys(b"\x15\x0c")
        wait_text("Billing migration")
        cancel()
        pump(0.5)
    (artifacts / "root-final.json").write_text(json.dumps(wt("roots", "keyboard-task"), indent=2))
    (artifacts / "setup-provenance.json").write_text(json.dumps(wt("setup-state", "list", "keyboard-task"), indent=2))
    # Rejecting cached recovery must stay in the choices, not retry transport.
    bad = artifacts / "sandbox/repos/offline-source"
    subprocess.check_call(["git", "init", "-q", "-b", "main", str(bad)])
    subprocess.check_call(["git", "-C", str(bad), "commit", "-q", "--allow-empty", "-m", "local-only"])
    subprocess.check_call(["git", "-C", str(bad), "remote", "add", "origin", str(artifacts / "sandbox/unavailable.git")])
    trace = artifacts / "cached-recovery.trace"
    subprocess.check_call(["tmux", "set-environment", "-t", "=keyboard-task", "GIT_TRACE", str(trace)])
    prefix("n")
    wait_text("Name this task")
    keys(b"\x15offline-cached-case\r")
    wait_text("Repositories")
    search("'offline-source")
    keys(b"\t\r")
    wait_text("Review")
    keys(b"\r")
    wait_text("Explicitly")
    before = trace.read_text().count("ls-remote")
    assert before > 0, "fixture did not attempt its explicit fetch"
    search("cached")
    keys(b"\r")
    wait_text("usable")
    wait_text("Explicitly")
    pump(0.5)
    assert trace.read_text().count("ls-remote") == before, "failed cached choice retried network"
    search("Cancel")
    keys(b"\r")
    pump(0.5)
    failed_root = wt("roots", "offline-cached-case")[0]
    assert not failed_root["checkouts"] and not failed_root["agents"][0]["native_id"]
    subprocess.check_call(["tmux", "set-environment", "-u", "-t", "=keyboard-task", "GIT_TRACE"])
    # Existing ambiguous records fail at the shared action boundary, including
    # the actual destructive picker callback, without touching either root.
    subprocess.check_call([os.environ["WT_STATE"], "set", root_id, "--agent", "pi"], stdout=subprocess.DEVNULL)
    for args in (("pick-preview", root_id), ("pick-delete", root_id), ("restore", root_id), ("label", root_id, "wrong root")):
        result = subprocess.run(["wt", *args], text=True, capture_output=True)
        assert result.returncode != 0 and "ambiguous root reference" in result.stderr, (args, result.stdout, result.stderr)
    subprocess.check_call(["tmux", "has-session", "-t", "=keyboard-task"])
    for name in ("keyboard-task", root_id):
        subprocess.check_call([os.environ["WT_STATE"], "get", name], stdout=subprocess.DEVNULL)
    assert wt("agents", "list", "keyboard-task")[0]["native_id"] == original["native_id"]
    print(f"PASS actual keyboard popup acceptance WT_FZF_VIM={int(vim)}")
finally:
    client.terminate()
    client.wait(timeout=5)
    log.close()
    os.close(master)
