---
name: pr-verify
description: Help a human check a PR's behaviour locally during a review. Propose what's worth verifying, set up tests, repro scripts, or a before/after comparison against the base, then let the human run them and record what was observed. Use for "let me check this myself", "can we verify", "does this actually work", "reproduce", "compare with before".
---

# PR verify

The human wants evidence they've seen for themselves, not your opinion. Your job is to make each check cheap to run and its result unambiguous.

## 1. Propose

Unless the human has named a check, propose 2 to 5 from: claims in the PR description, the walkthrough's Hotspots and Check questions, branches the new tests don't cover, and error paths. For each, give:

- what it proves, and what result would be a problem;
- the command, or what you'd write;
- what it costs (seconds, or needs a running stack, a database, credentials).

Let them pick, then do one check at a time.

## 2. Set up

- Find how this repo runs things in its own `AGENTS.md` or `CLAUDE.md`, Makefile, `package.json`, or CI workflows. Prefer the narrowest command: one test file or one test, not the suite.
- The strongest evidence is a test that fails at the PR head and would pass with a fix, or one that pins a claim from the description. Write such tests and repro scripts in `scratch/`, and point the runner at them from there. If the repo's test runner only finds tests inside the repo, copy the test in, run it, and delete it; tell the human you did.
- **Before/after**: to compare with the code before the change, add the base as a second checkout. Take `<base_sha>` from `review/layers.tsv`; it is the bottom layer's base, or the layer below the PR in question:

  ```sh
  wt add <Repo>@<base_sha>:<Repo>-base     # from the workspace root
  ```

  Then run the same command in both checkouts. Run `wt kit review` afterwards so `AGENTS.md` lists the new checkout.
- Env files the repo syncs through `.wt/sync` are already in the checkout. If something needs secrets or services that aren't there, say what's missing rather than working around it.
- In Rex, run anything slow or noisy in its own pane, so the human can watch it and your context stays small:

  ```sh
  rex run -s "$REX_SESSION" -c "<checkout>" --label "<check>" --keep-open --focus=false --json -- <command>
  ```

  Read it back with `rex capture -s "$REX_SESSION" -b <block> --trim`.

## 3. Run and record

When the human wants to run it themselves, give them the exact command and what to look for, and wait. Otherwise run it and show the relevant lines of output. Either way, add an entry under `## Verified` in `review/NOTES.md`:

```markdown
- **<claim or question>**: <confirmed | refuted | inconclusive>. `<command>` in `<checkout>`
  → <observed result, the key lines>. <what it means for the review>
```

Inconclusive is a fine result. Say what would settle it.

## Safety

Local only. Don't touch staging, production, customer, or shared environments, or spend cloud resources, without explicit approval for that environment. Don't change git state in the review checkout beyond a reverted experiment. Clean up only what you created.
