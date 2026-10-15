# Sessions

Every session has a mode:

- **on-record**: snapshotted into `evidence/`; statements may be attested from it.
- **off-record**: snapshotted, if at all, into `off-record/sessions/`; nothing in it can serve as
  evidence for an attestation. Off-record is about epistemic weight, not secrecy. It is a relief to
  the human, who does not have to guard every word against being written into the record.
- **private**: never recorded anywhere.

## Modes

**Kent Rule 10.** A mode belongs to the session, not to a turn. Do not decide it turn by turn
from your reading of what was said.

**Kent Rule 11.** Once recorded, a session keeps its mode and its filter profile, and a
document keeps its mode. `kb` refuses to change either; a different mode means a different
session.

**Kent Rule 12.** A session nobody declared is off-record. A session that never touches the
knowledge base, or only reads it, can stay that way. When statements worth recording come up, ask
the human whether the session goes on the record before snapshotting it as on-record. Where this
project's settings say to ask up front, ask before doing anything else.

**Kent Rule 13.** Never snapshot a private session.

**Kent Rule 14.** Attestations and agent statements come only from on-record sessions, and so
do the turns that mark an agent statement seen or withdraw it.

**Kent Rule 15.** A statement may cite off-record material as background. The citation is kept
as given, but it cannot be followed while the material stays off-record, and it adds no credence,
like a claim resting on an anonymous source.

**Kent Rule 16.** A decision reached off the record enters the record as a new on-record turn by
the human: "As discussed in <off-record session>, I have decided that ...". That turn attests
itself. From off-record work, collect the choices worth recording and propose going on the record,
or propose a prompt for a fresh on-record session that does not drag in the whole history.

## Forks

The history of a turn in a forked session is its ancestry chain: the fork's own earlier turns, then
the parent's turns up to the branch point, and so on up. Turns the parent gained after the branch
point are a sibling branch, not part of that history.

**Kent Rule 17.** A session may be on-record only if its whole fork ancestry is on-record. An
off-record session may fork from anything. Off-record material leaves its lineage by one of two
doors: the human deliberately puts whole sessions on the record, parent before fork; or a clean
break, a new on-record session with no fork parent, opened by the human's own framing turn, which
may cite the off-record background without carrying it as history.

## What enters the record

`kb snapshot <session>` reads a finished Claude Code session file and records its turns under a
filter profile; the default keeps what the human could see. Snapshot again as the session grows;
each re-snapshot adds the new turns. The recorder trusts the front end: the human is the key
`--user`, `KB_USER` or `~/.kenttheclerk.json` names, or else the person whose email in the user table
is git's `user.email` (Kent Rule 57). Nothing verifies it, and every act records its author as
asserted.

**Kent Rule 18.** What counts as evidence is what was shown. A turn of yours may contain hidden
reasoning and tool output the human never saw; when the human answers "that's right", they are
answering the visible reply. Read every human response in light of what they were looking at.
