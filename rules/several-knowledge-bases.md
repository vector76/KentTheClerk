# Several knowledge bases

Topics shared with different people go in separate knowledge bases, since a knowledge base is shared
whole. A code project can refer to several of them: `KENT_KB` in its settings names each folder,
separated as `PATH` separates folders, and the session-start text lists them. Each knowledge base
has an id, `K-` and twelve characters, in the committed file `kent.id` at its top; it never changes,
and every copy of the knowledge base carries it. A readable name for a knowledge base comes from
outside it, such as its repository's name, and is not stored beside the id.

**Kent Rule 59.** A session is on the record in at most one knowledge base, and counts as evidence
only there. In a project that refers to several, ask the human which one when the session goes on
the record, and pass `--kb <folder or id>` to the first `kb snapshot`. From then on `kb` finds the
knowledge base from the session: a re-snapshot, `kb attest`, `kb record`, `kb seen` and
`kb withdraw` go where the session is recorded, and `kb` refuses to record it in a second one. Any
other command that writes or reads one knowledge base takes `--kb`; `kb check` without it checks
each. When the knowledge bases are pinned to different versions of Kent, pass `--kb` on every
command, so that the launcher runs the version the knowledge base it names is pinned to.

**Kent Rule 60.** A session on the record in one knowledge base may read the others and cite them.
A statement in another knowledge base is written `<its id>/<statement id>`, as in
`K-4QX7M2PT9WNB/S-Y17H90`, in `rests_on`, in `context_for`, or in the text. A link with `context_for`
never marks its target, since a session on the record here cannot write there, so a reader of the
target does not learn of the link from it. Supersession, amendment and clarification stay within one
knowledge base, since each marks its target. `kb attest` and `kb record` refuse a reference they
cannot follow. A reference can be followed only by someone who has that knowledge base: each
person's `~/.kenttheclerk.json` maps ids to folders under `knowledge_bases`, for those the project
does not name, and `kb check` notes, without warning, the references it cannot follow. `kb links`
shows how far a reference can be followed, and maps a statement in another knowledge base given as
`<its id>/<statement id>`.
