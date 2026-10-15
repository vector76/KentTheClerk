# Developing Kent the Clerk

This repository is Kent the Clerk: the recorder `kb`, its launcher, and the rules and documents built
into it. The intent behind it lives in its own knowledge base, convkb, the folder `KENT_KB` names:
changes start there as decisions and are rendered here as code and prompts (S-0099). Sessions about
Kent run in this folder, the way a customer's sessions run in their project, and record into convkb.

## Knowledge base

This project keeps a knowledge base grounded in conversations: what was decided, why, and
according to whom. It is one capability of this project, not the whole of it. The recorder is the
`kb` command, part of Kent the Clerk, which is installed outside this repository with `kb` on the
PATH. The knowledge base is the folder named by `KENT_KB`, set per user in this project's
`.claude/settings.local.json`. Run `kb <command>` in exactly that form with nothing before it, since
the project's permissions allow that form. `kb where` prints where Kent and the knowledge base are,
and which version of Kent ran; below, `<kb>/` stands for the knowledge base's folder. Kent's rules and the
documents you read are built into `kb`: `kb rules` lists them, `kb rules <name>` prints a document,
`kb rules <number>` prints one rule, and a refusal names the rule it enforces and how to view it.
Read a document when its topic comes up, not before.

Two rules hold before any of those documents is read:

- **Write to the record only through `kb`.** Never create or edit anything in `<kb>/evidence/`,
  `<kb>/attested/` or `<kb>/agent/` by hand, not even to fix a typo (Kent Rule 1).
- **A session nobody declared is off-record.** When statements worth recording come up, ask the
  human whether this session goes on the record before recording anything (Kent Rule 12).

| When | Run |
|---|---|
| The human is new, or asks why the knowledge base works as it does | `kb rules introduction` |
| Answering a question from the knowledge base | `kb rules answering` |
| Proposing, drafting or attesting a statement | `kb rules attestation`, then `kb rules procedure` |
| Recording a choice you made as an agent statement | `kb rules agent-statements`, then `kb rules procedure` |
| Session modes, forks, snapshots, off-record material | `kb rules sessions` |
| The layers of the record, citing, documents, changing a statement | `kb rules record` |
| Linking statements, or reading one that carries `extra_context: true` | `kb rules links` |

Settings for this project:

- Ask about the session mode: up front. The knowledge base is central to this project's work, so if
  the human has not said which mode this session is in, ask before doing anything else.
- Statements to present per turn: one at a time.
- Output is priced in the human's audit time. Do not generate documents nobody asked for; prefer a
  short restatement the human can attest over a long summary they can only nod at.

## Working on Kent

- **Source.** The recorder is in `internal/kb/`, the launcher in `cmd/launcher/`. The rules are
  numbered in `rules/` and readable through `kb rules`; `<kb>/render-notes/kent-the-clerk.md` maps each
  rule to the statements it rests on, and is updated when a rule changes. The former Python recorder
  was retired at 0.8.0 and is gone.
- **Building.** After changing the source, rebuild and install with `./build-dev.ps1 -Install`. It
  installs a development build named after the version being worked on, such as `0.10.0-dev`, whose
  acts record the commit they were made under; a plain `go build` would stamp acts as if made under a
  release. `kb where` says which version ran. Run `go test ./...` and `kb check` before committing.
- **Versions.** Every attestation, agent statement, snapshot and ingest records the system it was made
  under (S-0030): `system_version`, set by hand as `defaultVersion` in `identity.go`, and `system_hash`,
  a content hash over the product's files (`kb version --files` lists them; this file is not among
  them). The version is bumped when work on the next version starts (S-0112).
- **Releasing.** Pushing a tag `vX.Y.Z` that matches `defaultVersion` runs the release workflow, which
  builds every platform and publishes the zips with the release's own install scripts (S-0116). Read
  the full message of every commit going into a release before tagging it. The repository is private,
  so the human downloads the release's `install.ps1` and Windows zip, and it is installed with
  `install.ps1 -ZipFile <zip>`.
- **Commits.** Commit straight to `main` here and to `master` in convkb (`git -C <kb>`), with no
  `Co-Authored-By` or other attribution lines; a `commit-msg` hook in both repositories refuses them.
- **User id.** It comes from `--user`, `KB_USER` or `~/.kenttheclerk.json`, and otherwise from git's
  `user.email` in the knowledge base's folder (S-0123). Here it is `jamie`, set in
  `~/.kenttheclerk.json`.
