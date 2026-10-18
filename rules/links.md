# Links between statements

A statement can relate to another in several ways, and each way has its own mechanism. Once a
statement is attested or recorded, its own file takes in only what changes its standing: that it
was superseded, amended or clarified, further attestations, and, for an agent statement, that it was
seen or withdrawn. The one exception is the mark `extra_context: true`, an objective fact that
tells a reader the file is not the whole picture. Everything else about a link, including who made
it and why, lives in the statement that makes it.

## Changing a statement

**Kent Rule 46.** Only a statement's author supersedes, amends or clarifies it: the person whose
attestation created it. Supersession means "I changed my mind", and only the author can say that.
When someone else says something that diverges from a statement, updates it, or reads it
differently, theirs is a second voice, not a replacement. They link to it with `context_for`, and
both stand on the record for whoever weighs them. The author may later supersede their own
statement. Any human may supersede an agent statement, overriding or adopting it (Kent Rule 37).
`kb attest` refuses a supersession, amendment or clarification of a human statement by anyone else.

## Linking

**Kent Rule 47.** Link with `context_for` only when a reader of the other statement would read it
differently after seeing yours: an update, a divergence, a qualification, a different context. Each
entry names the statement and carries a note, one line of at most 160 characters, saying how yours
changes its reading. The note is the link's type, read when someone follows the link, so write it for
that reader. `kb` marks the other statement `extra_context: true` and writes nothing else into it. A
human statement is never context for an agent statement; it supersedes the agent statement or leaves
it alone. An agent statement may be context for either kind.

**Kent Rule 48.** Every other relation has its own way, or none. To agree with a statement, endorse
it: a further attestation on the statement itself. To build on it, list it in `rests_on`, which does
not mark it, since depending on a statement does not change how it reads. Otherwise mention it by id
in the text, or not at all; a mention makes no link. A statement that both builds on another and
changes how it reads lists it in both `rests_on` and `context_for`. Both reach a statement in another
knowledge base too, where `context_for` marks nothing (Kent Rule 60).

## Reading

**Kent Rule 49.** When a statement you read carries `extra_context: true`, run `kb links <id>`
before relying on it: other statements change how it reads, and its file does not say which.
`kb links` maps the neighbourhood of one or more statements in one call. For each it gives the file,
title and standing, and the links in both directions, with their notes, and it follows supersession
to the version in force. Map first, then read the few files that matter together, rather than opening
statements one at a time. `--depth 2` goes a link further.
