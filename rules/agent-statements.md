# Agent statements

An agent statement records a working choice you made: an extrapolation of human intent, tentative
but operational until the humans decide otherwise. Its author is you and your model, and it has no
attestations; it is not testimony. Agent statements are numbered separately, `A-####`, and live in
`agent/`. Recording them keeps agents' behavior coherent from session to session, lets smaller
choices be settled once, and spares the humans decisions they do not need to make. Agent statements
may cite each other, which is how a rich set of implications gets built on a thin statement of human
intent.

## Recording

**Kent Rule 30.** Record an agent statement when a choice will come up again and could otherwise
go two ways. That includes a choice that is hard to reverse, one that would surprise a later reader
without context, or one made on a real trade-off; it also includes a coin flip that other work now
depends on. Emphasise what and why over how. This is a direction, not a test.

**Kent Rule 31.** An agent statement comes only from an on-record conversation, never from a
document, because the conversation gives the why. Off the record, collect candidate choices and
propose going on the record, or propose a prompt for a fresh on-record session.

**Kent Rule 32.** An agent statement is recorded from a turn of your own in which you state the
choice. Showing it in full there is preferred; if you do, it is recorded as seen.

## Weight and relying on them

**Kent Rule 33.** Present agent statements as working choices in force, never as something a
human attested. The least weight goes to one no human has seen. A human statement that cites an
agent statement confers some weight on it, short of endorsement.

**Kent Rule 34.** When you rely on an agent statement no human has seen, reproduce it in full in
the conversation and record that it was seen with `kb seen`, citing a turn that shows the full text.
Once seen, a summary will do.

## Revising

**Kent Rule 35.** You may revise an earlier agent's choice with judgment, by recording a new
agent statement that supersedes it. A choice unlikely to be depended on can be changed quietly. A
change that touches human intent is raised with the human first: they must be able to trust that
agent statements will not shift out from under them. `kb withdraw` retires one with nothing in its
place, and says why. Only an agent statement still in force can be revised or withdrawn. You never
revise a human statement; when a choice of yours changes how one reads, link to it with
`context_for` (Kent Rule 47).

**Kent Rule 36.** Keep extrapolations within the participants' shared intent. If a choice
contravenes what one human intended, warn that someone intended otherwise and ask before going
ahead; the warning is mainly for the human, who may recognise their own mistake.

**Kent Rule 37.** An agent statement carries no attestations. A human overrides or adopts one by
attesting a human statement that supersedes it; a human statement never amends or clarifies an agent
statement, and is never context for one. When a human agrees with an agent
statement outright, it becomes a human statement that way, so the two kinds stay separate.

`kb check` warns when an agent statement rests on a statement no longer in force. That list is where
cleanup starts.
