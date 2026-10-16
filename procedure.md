# Procedure

`kb` is Kent the Clerk's command, on the PATH. It is a launcher: it runs the version of Kent the
knowledge base is pinned to, or else the one `KENT_VERSION` names in the project's settings, or else the
highest version installed. It operates on the knowledge base named by the environment variable
`KENT_KB`, or, if that is unset, the one found in the current folder or above; `kb where` prints where
Kent and the knowledge base are and which version ran. Paths to the record in
these files, such as `attested/` or `off-record/drafts/`, are inside the knowledge base, and a
relative draft path given to `kb` is read from there. The human's id is their key in the knowledge
base's user table (`kb rules people`), from `--user`, `KB_USER`, or `~/.kenttheclerk.json`
(`{"user": "<key>", "folders": {"<folder>": "<key>"}}`, where an entry for the folder holding the
knowledge base, or one above it, wins over `user`), and otherwise from git's `user.email` as git
reports it in the knowledge base's folder, looked up in the table. Someone not in the table is added
before they act.

Run `kb` exactly as the instructions printed at the start of the conversation write it,
`kb <command>`, as a command of its own with nothing before or after it: the project's permissions
allow that form, and a prefix such as `export ...;`, a pipe or a second command makes it ask the human
again. Write drafts with your file-writing tool, not
through the shell; the permissions
allow writing in `off-record/drafts/` and forbid editing `attested/`, `evidence/` and `agent/`.

## Attesting a statement

1. Draft it in `off-record/drafts/<slug>.md` with frontmatter `title`, `answers` (the question it
   answers and the role it plays), and `cites` (background pointers). A draft that changes an
   existing statement also carries `supersedes: S-xxxx`, `amends: S-xxxx` or `clarifies: S-xxxx`,
   each naming a single statement; only that statement's author may attest such a draft. It may
   carry `rests_on` (statement ids it depends on) and `context_for`, a list of the statements whose
   reading it changes, each with a note of one line:

   ```
   context_for:
     - statement: S-0012
       note: holds only for exports since the migration
   ```
2. Post the exact final text as a clean block and ask for agreement, in the last message of your
   turn, after any tool calls. Text written before a tool call in the same turn may not reach the
   session file, and a proposal the snapshot does not contain cannot be attested.
3. After the human agrees, snapshot the session so both turns are recorded: `kb snapshot`. With no
   session given, it snapshots this conversation. It prints the pointer of the last human turn and of
   your last text before it; check that they are the agreement and your proposal.
4. Attest:
   `kb attest <draft> --proposed <session>#<your-turn-uuid> --turn <session>#<their-turn-uuid>`.
   Add `--form agreed-with-modification` when they agreed subject to a change (apply it to the draft
   first; the agreeing turn is the one that states the change); `--form self` when their own turn
   contains the text (omit `--proposed`); `--form endorsement --statement S-xxxx` to add a further
   attestation to an existing statement.
5. `kb check`, then `kb index`.

To find turn uuids, `kb show <session>` prints every turn with its pointer. `kb snapshot` takes this
conversation's id from `CLAUDE_CODE_SESSION_ID`, which Claude Code sets but does not document, and
refuses if that conversation's file is not being written right now, as can happen after `/clear`.
If it refuses, find this conversation's file by searching the project's session files for words
from the human's latest message, and pass its id.

## Recording an agent statement

1. State the choice in a turn of your own, in an on-record session, in the last message of that
   turn, for the same reason as above.
2. Draft it in `off-record/drafts/` with `title`, `answers`, optional `rests_on`, `context_for` and
   `cites`. A revision of an agent statement carries `supersedes: A-xxxx`.
3. `kb snapshot`, then `kb record <draft> --turn <session>#<your-turn-uuid>`.
4. When a human is later shown one in full: `kb seen A-xxxx --turn <ptr>`. To retire one with nothing
   in its place: `kb withdraw A-xxxx --turn <ptr> --reason <text>`.

## Commands

```
kb snapshot [<session-id|path>] [--mode on-record|off-record] [--filter human-visible-v1|full-v1]
kb ingest <file> [--mode on-record|off-record] [--extractor text-v1|html-v1|pdf-v1|docx-v1|opaque-v1] [--title T] [--caption C]
kb show [<session>[#<uuid>] ...] [--human-only]
kb attest <draft> --turn <ptr> [--proposed <ptr>] [--form proposed-and-agreed|agreed-with-modification|self|endorsement] [--statement S-xxxx]
kb record <draft> --turn <ptr>
kb seen A-xxxx --turn <ptr>
kb withdraw A-xxxx --turn <ptr> --reason <text>
kb override merge|reassign|scrub|user <args> [--confirm --turn <ptr> --reason <text>]
kb override revise S-xxxx <draft> --proposed <ptr> --turn <ptr> --reason <text> [--form ...]
kb links <S-xxxx|A-xxxx> ... [--depth N]
kb check
kb index
kb search <terms>
kb serve [--port 8765]
kb version [--files]
kb where
kb rules [<number>|<name>]
kb pin [--set | --remove]
kb --launcher [version]
```

`kb links` maps the links around one or more statements in one call: each statement's file, title
and standing, and its links in both directions with their notes (Kent Rule 49). `kb check`
re-verifies every recorded act against the rules. A clean write path never makes it
fail; it exists for the day someone touched the files by hand. `kb serve` renders the knowledge base
as linked HTML for reading. `kb where` prints the folder Kent is installed in and the
knowledge base `kb` operates on, with how it was found. `kb rules` lists Kent's rules and the
documents built into `kb`, this one included. `kb pin` shows the version of Kent the knowledge base is
pinned to, if any; run `--set` or `--remove` only when the human asks. `kb --launcher` is answered by the launcher itself: the versions installed, and which one it
would run and why.

## Vocabulary

**Evidence**: the raw record of sessions and documents. **Attest / attestation**: the act of a human
confirming a statement, and its record. **Attested**: the state of a statement with at least one
attestation. **Agent statement**: a working choice recorded by an agent. **On-record**: material
statements may be attested from. **Off-record**: recorded material nothing can be attested from.
**Private**: never recorded. **Supersede**: replace a statement's currency with a newer one, which
only its author does. **Context for**: a link from one statement to another whose reading it
changes; it marks the other `extra_context`.
**Promotion** is plain language for movement from off-record or draft material to attested, not a
term of art. Avoid *ratified*, *canon*, *queue*, *tentative*, and *incognito*.

## Bindings

- Claude Code session files: `~/.claude/projects/<project>/<session-id>.jsonl` (override with
  `CLAUDE_PROJECTS_DIR`).
