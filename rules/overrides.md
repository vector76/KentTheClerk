# Overrides

Some situations call for changing the record against its own rules: a person recorded under a key
that was wrong from the start, content that cannot stay for legal reasons or because it is a secret or
someone's personal data, a session that should never have been recorded, a statement attested
backwards and caught before anything relied on it. Rather than leave people to edit files by hand,
export and re-import, or rewind the files, `kb` offers a closed list of overrides. Each is entered
in the override log, `overrides.jsonl` at the top of the knowledge base, which `kb` only appends
to and `kb check` verifies, so the record is as it was written except where the log says otherwise.

| Override | Command | What it changes |
|---|---|---|
| Merge two keys | `kb override merge <from> <into>` | every act by `<from>` is recorded as by `<into>`, which has a row in the user table; it also puts right a key wrong from the start |
| Reassign an act | `kb override reassign <session>#<uuid> <key>` | one human turn, and the attestations made through it |
| Scrub | `kb override scrub <session>#<uuid>`, `<session>` or `S-xxxx` | the content of a turn, a whole session or document, or a statement, leaving a tombstone |
| Revise | `kb override revise S-xxxx <draft> ...` | a statement nothing relies on yet, attested anew |
| Change a person's row | `kb override user <key> --name <name> --email <email>` | their name or email in the user table, never their key (Kent Rule 58) |

## Making an override

**Kent Rule 50.** An override is a last resort, not a normal mechanism. Use only those on the
list, and only through `kb override`; any other change to the record is still forbidden (Kent
Rule 1). Never propose one to settle a disagreement, to act as someone else, or to change what a
statement said once something relies on it: those have their own ways (Kent Rules 4, 47 and 48).

**Kent Rule 51.** An override is made from an on-record session, so that the conversation around
it is on the record too, and on the authority of a turn of the human's own in it, which its log
entry cites. `kb` refuses an override from an off-record session. In a knowledge base in git, which
is cooperative, anyone may make one; where there is an administrator, only the administrator may.

**Kent Rule 52.** Every override but a revision is checked twice. Run it first without
`--confirm`: `kb` prints what it would change and a warning, and changes nothing. Show the human
all of that, and ask them to confirm that it is really what they want. Only after they confirm in a
turn of their own, snapshot the session and run the same command with `--confirm`, `--turn`
naming that turn and `--reason` saying why. `kb` refuses a confirming turn made before the check.

## Revising a statement nothing relies on

**Kent Rule 53.** A statement may be revised in place, in meaning as well as wording, while
nothing relies on it, like a commit that has not been pushed. Something relies on it once another
statement rests on it, supersedes, amends or clarifies it, links to it with `context_for` or
mentions its id; once someone else has endorsed it; or once it has been pushed to the knowledge
base's upstream. Only the person who attested it revises it, with a reason, and the revision is
proposed and agreed like any statement (Kent Rules 19 to 29): `kb override revise` takes the draft
and the turns as `kb attest` does. It needs no double-check. The statement keeps its id, and the
log keeps its original text. Whether the statement was relied on outside the record, for instance
already turned into code, `kb` cannot see: ask the human before revising one that might have been.

## What a scrub does not reach

**Kent Rule 54.** A scrub replaces content in the knowledge base with a tombstone saying when, by
whom and why, and rebuilds the index. It does not reach copies outside the knowledge base: git
history and every clone of it, the session file a conversation was snapshotted from, and a scrubbed
statement's text in the turns where it was proposed and agreed, which are scrubbed separately.
`kb` lists where the content may remain; tell the human, whose decision it is to remove it there.
A statement resting on scrubbed evidence stays in force; `kb check` flags it for review.
