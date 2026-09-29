<!-- Knowledge base: paste this block into the project's CLAUDE.md. -->

## Knowledge base

This project keeps a knowledge base grounded in conversations: what was decided, why, and
according to whom. It is one capability of this project, not the whole of it. The recorder is the
`kb` command, part of Kent the Clerk. Kent is installed outside this repository, in the folder named
by the environment variable `KENT_HOME`; the knowledge base is the folder named by `KENT_KB`. Both
are set per user in this project's `.claude/settings.local.json`. Run `kb` as
`python "$KENT_HOME/kb.py" <command>` (in PowerShell, `python "$env:KENT_HOME/kb.py" <command>`).
`kb where` prints both folders; below, `<kent>/` and `<kb>/` stand for them. Kent's rules are in
`<kent>/rules/`, one file per topic. Read a file when its topic comes up, not before.

Two rules hold before any of those files is read:

- **Write to the record only through `kb`.** Never create or edit anything in `<kb>/evidence/`,
  `<kb>/attested/` or `<kb>/agent/` by hand, not even to fix a typo (Kent Rule 1).
- **A session nobody declared is off-record.** When statements worth recording come up, ask the
  human whether this session goes on the record before recording anything (Kent Rule 12).

| When | Read |
|---|---|
| The human is new, or asks why the knowledge base works as it does | `<kent>/README.md` |
| Answering a question from the knowledge base | `<kent>/rules/answering.md` |
| Proposing, drafting or attesting a statement | `<kent>/rules/attestation.md`, then `<kent>/procedure.md` |
| Recording a choice you made as an agent statement | `<kent>/rules/agent-statements.md`, then `<kent>/procedure.md` |
| Session modes, forks, snapshots, off-record material | `<kent>/rules/sessions.md` |
| The layers of the record, citing, documents, changing a statement | `<kent>/rules/record.md` |

Settings for this project:

- Ask about the session mode: when record-worthy statements come up. (Change to "up front" if the
  knowledge base is this project's main work.)
- Statements to present per turn: one at a time. (A preference of the human; change it to theirs.)
