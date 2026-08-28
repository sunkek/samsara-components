# Issue tracker

Issues and pull requests for this repository live on GitHub, in
[`sunkek/samsara-components`](https://github.com/sunkek/samsara-components).
The repository is public, so reads work without authentication; writes need a
`gh` login.

Use the `gh` CLI rather than the web UI or raw `curl`, so output is scriptable
and the repository is inferred from the remote.

## Reading

Issue references in commit messages and PR bodies take the usual GitHub forms —
`#123`, `Closes #123`, `Fixes #123`, and the cross-repo `sunkek/samsara-components#123`.
The number is the identifier; resolve it with:

```bash
gh issue view 123                       # title, body, labels, state
gh issue view 123 --comments            # with the discussion
gh issue view 123 --json title,body,labels,state,milestone
```

Pull requests share the number space with issues, so a reference that `gh issue
view` reports as not found is usually a PR:

```bash
gh pr view 123 --json title,body,state,files
```

To find the spec behind a branch when no commit message names one:

```bash
gh pr list --search "head:<branch>" --state all --json number,title,body
gh issue list --search "<keyword>" --state all --limit 20
```

## Writing

Only when the user asks for it. Issue and PR text is read by humans, so write
it in normal prose, and follow the commit and PR conventions in
[AGENTS.md](../../AGENTS.md) — summary, tests for behaviour changes, linked
issue, and updates to the module `README.md` and the root `CHANGELOG.md` when a
public API or config field moves.

```bash
gh issue create --title "postgresql: ..." --body-file <path>
gh pr create --title "postgresql: ..." --body-file <path>
```

Scope the title to a module or a behaviour, the same way commit subjects are
scoped.
