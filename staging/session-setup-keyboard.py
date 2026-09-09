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
    if vim:
        # Prove mode-sensitive Esc with real fzf: initial NORMAL aborts, while
        # INSERT Esc returns to NORMAL and the following Esc aborts.
        prefix("n")
        wait_text("Name this task")
        keys(b"\x15esc-one\r")
        wait_text("Repositories")
        wait_text("NORMAL")
        keys(b"\x1b")
        pump(0.5)
        assert wt("roots", "esc-one") == []

        prefix("n")
        wait_text("Name this task")
        keys(b"\x15esc-two\r")
        wait_text("Repositories")
        wait_text("NORMAL")
        keys("/")
        wait_text("INSERT")
        keys(b"\x1b")
        wait_text("NORMAL")
        keys(b"\x1b")
        pump(0.5)
        assert wt("roots", "esc-two") == []

        prefix("n")
        wait_text("Name this task")
        keys(b"\x15h-abort-task\r")
        wait_text("Repositories")
        wait_text("NORMAL")
        keys("h")
        pump(0.5)
        assert wt("roots", "h-abort-task") == []
    prefix("n")
    wait_text("Name this task")
    keys(b"\x15keyboard-task\r")
    wait_text("Repositories")
    if vim:
        wait_text("NORMAL")
        # Ctrl-D remains paging, never deletion.
        keys(b"\x04")
    search("'alpha")
    keys(b"\t")
    search("'beta")
    keys(b"\t")
    # Manual entry returns with exactly the same two live marks.
    keys(b"\x0f")
    wait_text("Full working repository path")
    keys(str(artifacts / "sandbox/repos/alpha") + "\r")
    wait_text("Repositories")
    keys("l" if vim else b"\r")
    wait_text("Review")
    # Review can return to the picker without losing either live mark.
    keys("jj" if vim else b"\x1b[B\x1b[B")
    keys(b"\r")
    wait_text("Repositories")
    keys("l" if vim else b"\r")
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
        picker_calls = call_log.read_text()[len(calls_initial):]
        if "worktree projection names" in picker_calls:
            break
        pump()
    else:
        raise AssertionError("picker initial name-list call bypassed tracing")
    # Rich details are visible before any opt-in key in both responsive modes.
    wait_text("Identity:")
    pump(0.5)
    keys("?")
    pump(0.5)
    hidden_calls = call_log.read_text()
    keys("j" if vim else b"\x1b[B")
    pump(0.7)
    hidden_delta = call_log.read_text()[len(hidden_calls):]
    assert "worktree projection preview" not in hidden_delta
    assert "worktree projection show" not in hidden_delta
    keys(b"\x10")
    wait_text("Identity:")
    if vim:
        # INSERT restores literal printable input, including the picker-specific ?.
        keys("/")
        wait_text("INSERT")
        keys("?")
        wait_text("0/")
        keys(b"\x1b")
        wait_text("NORMAL")
    # Back in NORMAL (or ordinary non-Vim mode), ? toggles details off again.
    keys("?")
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
    picker_calls = call_log.read_text()[len(calls_initial):]
    assert picker_calls.count("worktree projection names") == 1, picker_calls
    assert "worktree projection rows" not in picker_calls
    assert "worktree projection list" not in picker_calls
    valid_ids = {candidate["id"] for candidate in wt("roots")}
    callbacks = []
    for line in picker_calls.splitlines():
        parts = line.split()
        if len(parts) == 4 and parts[:3] == ["worktree", "projection", "preview"]:
            callbacks.append(("preview", parts[3]))
        elif len(parts) == 4 and parts[:3] == ["worktree", "projection", "show"]:
            callbacks.append(("show", parts[3]))
    assert callbacks and {kind for kind, _ in callbacks} == {"preview", "show"}, callbacks
    assert all(len(identity) == 32 and all(c in "0123456789abcdef" for c in identity) and identity in valid_ids for _, identity in callbacks), callbacks
    keys(b"\r")
    pump(0.7)
    current = subprocess.check_output(["tmux", "list-clients", "-F", "#{session_name}"], text=True).strip()
    assert current == "keyboard-task", current

    # Force the same picker through its portrait branch without rerunning the
    # setup suite. Default rendering and both toggles remain live at 150 columns.
    subprocess.check_call(["tmux", "set-environment", "-t", "=keyboard-task", "WT_STATE", os.environ["WT_STATE"]])
    subprocess.check_call(["tmux", "set-environment", "-t", "=keyboard-task", "WT_MOBILE", "1"])
    portrait_before = call_log.read_text()
    prefix("s")
    wait_text("Select session")
    wait_text("Identity:")
    assert "worktree projection preview" in call_log.read_text()[len(portrait_before):]
    keys("?")
    pump(0.5)
    portrait_hidden = call_log.read_text()
    keys("j" if vim else b"\x1b[B")
    pump(0.7)
    portrait_hidden_delta = call_log.read_text()[len(portrait_hidden):]
    assert "worktree projection preview" not in portrait_hidden_delta
    assert "worktree projection show" not in portrait_hidden_delta
    portrait_resume = call_log.read_text()
    keys(b"\x10")
    wait_text("Identity:")
    assert "worktree projection preview" in call_log.read_text()[len(portrait_resume):]
    cancel()
    pump(0.5)
    subprocess.check_call(["tmux", "set-environment", "-u", "-t", "=keyboard-task", "WT_MOBILE"])

    if not vim:
        # Delete under a filter, then clear it: other local candidates survive.
        subprocess.check_call(["wt", "new", "filter-delete-victim", "--offline"], stdout=subprocess.DEVNULL)
        prefix("s")
        wait_text("Select session")
        keys("?")
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
