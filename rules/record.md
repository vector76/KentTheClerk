# The record

The knowledge base records the positions, rationale and decisions that people and their agents
arrive at in conversation, each traceable to the person who said it and the moment they confirmed
it. It is domain-neutral. It starts empty and fills only through the process in these files.

The software has two parts. The **recorder** is a system of record in the manner of a county
recorder or notary: it enforces process and requires signatures, and does not interpret content.
It is the `kb` command, installed with Kent the Clerk outside the project's repositories. The
record lives in the knowledge base's own folder, and the locations below are inside it. The
**interpreter** is you, the agent: conversations happen and decisions get made with you, and
judgment about meaning and weight is yours. You read the record's files directly and search them
however you like, with `kb search` beside that. You write only through `kb`, which refuses what
breaks its rules and says why. The recorder detects no contradictions and weighs nothing.

| Layer | Location | What it is | Your access |
|---|---|---|---|
| Evidence | `evidence/*.jsonl` | on-record conversation snapshots and document projections; low trust | read, search, cite; append via `kb snapshot` and `kb ingest` |
| Originals | `evidence/originals/<sha256><ext>` | the bytes a document projection was taken from | read |
| Attested statements | `attested/S-*.md` | claims a named person confirmed at a named time; the only authoritative layer | read, cite; write via `kb attest` |
| Agent statements | `agent/A-*.md` | working choices recorded by an agent; operational, never testimony | read, cite, rest on; write via `kb record`, `kb seen`, `kb withdraw` |
| Override log | `overrides.jsonl` | the overrides made to the record against its rules, each with who, when and why | read; append via `kb override` |
| Off-record | `off-record/` (gitignored) | off-record sessions and drafts, outside the record; at risk of loss by design | read if present; draft here |
| Index and view | `index.db`, `render/` | derived, disposable | rebuild with `kb index` |

A statement and its attestations are separate things. A statement has an id, a title, a text, the
question it answers, an originator and citations. An attestation records who, when, the turn where
they agreed, the form of agreement, and the system it was made under. "Attested" always carries an
obvious *by whom*.

A document enters evidence as two things: its original bytes, named by their content hash with the
extension kept, and a projection of them into chunk records under a named extractor, the way a
conversation is recorded under a filter. Citations point into the projection.

## Writing

**Kent Rule 1.** Write to the record only through `kb`. Never create, edit, rename or delete a
file in `evidence/`, `attested/` or `agent/`, or the override log, by hand. Nothing mechanical stops
you; this rule does. Where the record must change against its rules, `kb override` offers a closed
list of ways to do it (Kent Rules 50 to 54).

**Kent Rule 2.** Evidence is append-only. A recorded turn is never dropped or altered, except
by an override (Kent Rule 50); a re-snapshot of a session only adds turns. Do not pass `kb snapshot --force` without the human's
say-so.

**Kent Rule 3.** A statement exists only because someone attested it. A pending proposal is a
draft in `off-record/drafts/` or a turn in the conversation, never a file in `attested/`.

## Changing a statement

**Kent Rule 4.** An attested statement's text never changes. To change what the record says, the
old statement's author attests a new one that supersedes, amends or clarifies it (Kent Rule 46), and
`kb attest` marks the old one. Anyone else who reads it differently links to it with `context_for`
instead (Kent Rule 47). Supersession replaces the old statement's currency; it stays on the record,
and what cited it is orphaned, not invalidated. An amendment adds to a statement and a
clarification states its scope or meaning; neither may contradict it, and if a change would make any
sentence of the original false, it is a supersession. When marks pile up on one statement, the next
change consolidates the original and its additions into one superseding statement.

**Kent Rule 5.** There is no exception for typo fixes. `kb` cannot tell whether a change leaves
the meaning unchanged, so a typo is fixed like any other change: the author supersedes the statement
(Kent Rule 4), or, while nothing relies on it yet, revises it in place (Kent Rule 53). When you
notice one in a human's statement, point it out; what to do about it is the author's decision.

## Citing

**Kent Rule 6.** Cite statements by id (`S-0012`, `A-0003`) and evidence by pointer
(`<session-id>#<turn-uuid>`). Never cite by heading, position, or a label a view made up.

**Kent Rule 7.** Never assert anything from evidence directly, and never summarise evidence in
place. Evidence supports statements; it is not one.

## Documents

**Kent Rule 8.** Nothing in a document can attest. A chunk may be cited as background and quoted,
but a proposal and an agreement are always turns in an on-record conversation. A document's author
is not a participant here and agreed to nothing. If a document says something worth attesting, the
human says it in an on-record turn, citing the chunk.

**Kent Rule 9.** A projection is frozen. `kb ingest` refuses the same bytes under the same
extractor; a better extractor makes a new evidence item pointing at the same original. Never edit or
rename an original.

## Storage

The record is plain files, meant to be kept in git. `kb` does not commit: committing is the human's
call, or yours when they ask. Running several copies of one record synced through git is not
supported.
