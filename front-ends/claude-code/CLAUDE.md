<!-- Knowledge base: paste this block into the project's CLAUDE.md. -->

## Knowledge base

This project keeps a knowledge base grounded in conversations: what was decided, why, and
according to whom. It is one capability of this project, not the whole of it. The recorder is the
`kb` command, part of Kent the Clerk. Kent is installed outside this repository, in the folder named
by the environment variable `KENT_HOME`; the knowledge base is the folder named by `KENT_KB`. Both
are set per user in this project's `.claude/settings.local.json`. Run `kb` as
`"$KENT_HOME/kb" <command>` (in PowerShell, `& "$env:KENT_HOME/kb" <command>`),
in exactly that form with nothing before it, since the project's permissions allow that form.
`kb where` prints both folders; below, `<kb>/` stands for the knowledge base's. Kent's rules and the
documents you read are built into `kb`: `kb rules` lists them, `kb rules <name>` prints a document,
`kb rules <number>` prints one rule, and a refusal names the rule it enforces and how to view it.
Read a document when its topic comes up, not before.

Two rules hold before any of those documents is read:

- **Write to the record only through `kb`.** Never create or edit anything in `<kb>/evidence/`,
  `<kb>/attested/` or `<kb>/agent/` by hand, not even to fix a typo (Kent Rule 1).
- **A session nobody declared is off-record.** When statements worth recording come up, ask the
  human whether this session goes on the record before recording anything (Kent Rule 12).

| When | Run `kb` as above with |
|---|---|
| The human is new, or asks why the knowledge base works as it does | `rules introduction` |
| Answering a question from the knowledge base | `rules answering` |
| Proposing, drafting or attesting a statement | `rules attestation`, then `rules procedure` |
| Recording a choice you made as an agent statement | `rules agent-statements`, then `rules procedure` |
| Session modes, forks, snapshots, off-record material | `rules sessions` |
| The layers of the record, citing, documents, changing a statement | `rules record` |

Settings for this project:

- Ask about the session mode: when record-worthy statements come up. (Change to "up front" if the
  knowledge base is this project's main work.)
- Statements to present per turn: one at a time. (A preference of the human; change it to theirs.)
