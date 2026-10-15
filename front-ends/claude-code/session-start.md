# Knowledge base

This project keeps a knowledge base grounded in conversations: what was decided, why, and
according to whom. It is one capability of this project, not the whole of it. This text was put here
when the conversation started, by the session-start hook in this project's local settings.

The recorder is the `kb` command, part of Kent the Clerk, which is installed outside this repository
with `kb` on the PATH. Run `kb <command>` in exactly that form with nothing before it, since the
project's permissions allow that form. Below, `<kb>/` stands for the knowledge base's folder, given at
the end with the version of Kent that runs. Kent's rules and the documents you read are built into
`kb`: `kb rules` lists them, `kb rules <name>` prints a document, `kb rules <number>` prints one rule,
and a refusal names the rule it enforces and how to view it. Read a document when its topic comes up,
not before.

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
| Setting Kent up in this project, or something below says it is not set up right | `kb setup` |

The hook runs again when a conversation is resumed or its context is compacted. A mode already
declared in this conversation still holds (Kent Rule 10).
