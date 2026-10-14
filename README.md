# Kent the Clerk: an introduction

Kent the Clerk keeps a knowledge base grounded in conversations. This is for you, the person who
will talk with an agent and build that knowledge base up. It explains what the knowledge base is
for, how it works, and why it works that way. Your agent reads this too, so what it says here is how
the system actually behaves.

Kent is the name and clerk is the role: a clerk keeps the record and is the front desk to it. In
conversation, your agent may go by Kent.

## What it is for

Most of the effort around AI agents goes into turning a clear specification into working results.
The harder part comes earlier: working out what people actually want, and keeping hold of it. That
is **intent**, and this knowledge base is where intent is stored.

Intent gets worked out in conversation. People rarely know exactly what they want at the start;
they discover it by discussing, trying things, and changing their minds. The knowledge base records
the positions, reasons and decisions that come out of those conversations. Each one can be traced to
the person who said it and the moment they confirmed it. When you or an agent later need to know
what was decided and why, the answer is there, with its source.

Building software is the example that motivated it, but nothing here is specific to software. The
knowledge base doesn't know or care what your conversations are about.

## The core idea: you talk, the agent distils, you confirm

You talk with your agent as you normally would. When something worth keeping comes up, such as a
decision, a position or a reason, the agent drafts it as a short **statement** and shows you the
exact text. If you agree, you say so, and the agent records your agreement. That act is called
**attesting**, and a statement you have attested is **attested**.

Only attested statements are authoritative. Everything else is background:

- **Evidence** is the raw record: your conversations, and any documents you add. It is kept word for
  word and can be cited, but nothing is asserted from it directly.
- **Attested statements** are what people confirmed, each pointing back to the conversation it came
  from.
- **Agent statements** are working choices the agent made on its own, recorded so they stay
  consistent. They are described further below.

The agent drafts, and you decide. The agent never promotes its own interpretation to an attested
statement.

## Agreeing is deliberate

Agreement has to be explicit, and it applies to exact text:

- **Silence is not agreement.** Neither is a vague nod at a long summary. If you don't say you agree
  to a specific statement, nothing is recorded.
- **You agree to the exact words shown.** The recorder checks that the statement matches, word for
  word, the text the agent showed you, and that your agreement came after it, in your own turn. If
  you want a change, say what it is; you can agree subject to that change, or ask to see the revised
  text first.
- **Statements come one at a time by default.** Reviewing several at once tends to mean the first
  gets all the attention. If you prefer more per turn, change the setting in your CLAUDE.md block.

This can feel slow at first. It is what makes an attested statement mean something: a specific
person agreed to specific words at a specific moment.

## A statement keeps its context

The same sentence means different things in different settings. Something you said while answering
a particular question, or as an example, or as a tentative position, becomes a stronger and
different claim when lifted out and stated on its own. So every statement says what question it
answers and what role it played: an example, a working position, a decision, or a belief stated
directly. It keeps your voice rather than being smoothed into neutral language.

This lets you agree without reservation to what you actually said, without being held to a broader
claim you never made.

## Nothing is edited; things are superseded

An attested statement's text never changes. When you change your mind, you attest a new statement
that **supersedes** the old one, and the old one stays on the record marked as superseded. You can
also **amend** a statement, adding to it, or **clarify** it, stating its scope or meaning, as long as
the addition doesn't contradict it.

So you can't simply ask the agent to fix a statement's wording, not even a typo. The agent will
propose a new statement, or raise the typo with you, and you decide. Keeping the history means anyone
can see what was believed when, and what replaced it.

## Session modes: on the record, off the record, private

Every conversation has a mode:

- **On-record**: recorded as evidence. Statements can be attested from it.
- **Off-record**: may be recorded for your reference, but nothing in it can be attested. It is kept
  outside the record, in a folder that is not committed to git, and can be lost.
- **Private**: never recorded by the knowledge base at all. (Claude Code keeps its own local session
  history as usual; the knowledge base simply never takes it in.)

Off-record is about weight, not secrecy. It gives you room to think out loud, try ideas, and say
things you might not stand behind, without any of it being written into the record or taken as tacit
approval. Many people find working this way a relief, and much good work happens off the record.

A conversation nobody labelled is treated as off-record. When something worth recording comes up,
the agent will ask whether to go on the record. (Your CLAUDE.md block can tell it to ask at the start
of every conversation instead.) If you reach a decision off the record, you bring it onto the record
by stating it yourself in an on-record conversation, for example: "As discussed earlier, I have
decided that ...". The agent may also offer to collect what's worth recording and propose a fresh
on-record conversation to capture it cleanly.

By default the record keeps what you could see. Your agent's hidden reasoning and tool output are
left out, because when you say "that's right", you are answering what was in front of you.

## Agent statements

Agents make many small choices while working for you, often ones that could reasonably go either
way. When such a choice is likely to come up again, the agent records it as an **agent statement**:
a working choice, in force until someone decides otherwise. Agent statements are numbered separately
(`A-0001` rather than `S-0001`), carry no attestations, and are never presented as something you
said.

- Before relying on an agent statement you haven't seen, the agent shows it to you in full.
- An agent may revise another agent's choice, but it raises any change that touches what you
  intended.
- You override an agent statement, or adopt it as your own, by attesting a statement of yours that
  supersedes it.

Recording these choices keeps the agent's behavior consistent over time and spares you decisions you
don't need to make.

## Disagreement is allowed

Attested statements are testimony, not axioms. Different people, or the same person at different
times, may say things that conflict, and the knowledge base keeps both rather than quietly merging
them. When a conflict starts to matter, the agent resolves it if there's an obvious answer, such as a
later statement by the same person, and tells you which it applied. Otherwise it asks you. You can
ask for a list of current contradictions at any time. Statements that answer different questions
don't conflict just because they differ.

## How the agent answers from the knowledge base

When you ask a question, the agent's answer is its own interpretation, supported by citations. It
keeps apart what attested statements say, what agent statements hold, what off-record material or
drafts lean toward, and its own reading. It weighs statements by what's recorded about them: your own
words carry differently from agreement to the agent's wording, for example. If the knowledge base
doesn't answer your question, the agent says so and can offer to draft a statement with you.

## Two parts: the recorder and the agent

The system has two parts with different jobs:

- The **recorder** is the `kb` command. Like a notary, it checks process and signatures and never
  interprets meaning. Every change to the record goes through it. When it refuses something, it
  says which of Kent's rules the refusal enforces and how to read it, for example "Kent Rule 25:
  `kb rules 25` to view it".
- The **agent** does the interpreting: the conversation, the drafting, the weighing. It reads the
  record's files directly and writes only through `kb`.

The rules the agent follows are built into `kb`, along with this page: `kb rules` lists them, and
`kb rules 25` prints one. They are the same for every
knowledge base on this version of Kent the Clerk. Nothing you record changes them; they change
only when a new version is adopted, and every recorded act notes the version it was made under.
Statements you attest about how your team should work are part of your content, and the agent weighs
them like any other statement.

## Getting set up and working day to day

- **Install Kent the Clerk once, outside your repositories,** the way you install a tool: download
  the zip for your platform from the project's GitHub releases, unpack it into a folder of your
  choosing, such as `%LOCALAPPDATA%\Programs\KentTheClerk` on Windows or
  `~/.local/share/kenttheclerk` on macOS and Linux, and add that folder to your PATH. The zip holds
  `kb` (`kb.exe` on Windows), which is a launcher, and the version itself in `versions/<version>/`;
  this page, the rules and the front-end block are built into the version. To add another version,
  unpack its zip into the same folder. `kb` runs the version your knowledge base is pinned to, or
  else the one `KENT_VERSION` names in your project's settings, or else the highest version
  installed; `kb --launcher` shows which, and why. Nothing of Kent goes into your code repository or
  your knowledge base, and nothing else needs installing. On macOS the executables are not
  notarized, so clear their quarantine flag once: `xattr -dr com.apple.quarantine <folder>`. To
  build from a source checkout instead, run `build-dev.ps1 -Install` (or `build-dev.sh --install`)
  in the checkout; it installs a development build beside the launcher.
- **Give your knowledge its own folder.** Usually it sits beside your code repository as a
  repository of its own; it can also sit inside the code repository. To start, create it with empty
  `attested/` and `evidence/` folders; `kb` looks for them to find the record. The record fills
  them, and `agent/`, as you go.
- **Tell your project where the knowledge is, and what the agent may do there,** in the code
  repository's `.claude/settings.local.json`:

  ```json
  {
    "env": {
      "KENT_KB": "C:/work/myproject-knowledge"
    },
    "permissions": {
      "additionalDirectories": ["C:/work/myproject-knowledge"],
      "allow": [
        "Bash(kb *)",
        "PowerShell(kb *)",
        "Edit(//c/work/myproject-knowledge/off-record/drafts/**)"
      ],
      "deny": [
        "Edit(//c/work/myproject-knowledge/attested/**)",
        "Edit(//c/work/myproject-knowledge/evidence/**)",
        "Edit(//c/work/myproject-knowledge/agent/**)"
      ]
    }
  }
  ```

  Set these there, for this project, not as environment variables for your whole user account:
  those would point every project at the same knowledge base. The file is yours and not committed,
  so each person on a team keeps their own paths. Claude Code keeps it out of git when Claude Code
  creates it; if you create it yourself, add `.claude/settings.local.json` to your `.gitignore`. To
  run a particular installed version of Kent for this project, add `"KENT_VERSION": "<version>"` to
  `env`; a pin in the knowledge base takes its place, and `kb` refuses if the two disagree.

  The deny rules stop the agent from editing the record's files directly, so only `kb` writes
  there. The other permissions let it read the knowledge base, run `kb`, which also serves Kent's
  rules, and write drafts. Permission rules take paths in their own form and don't expand variables, so write your
  folders out in full: `C:\work\myproject-knowledge` becomes
  `//c/work/myproject-knowledge`, and on macOS or Linux `/home/you/knowledge` becomes
  `//home/you/knowledge`.
- **Work in Claude Code's auto mode.** It is required: without it, the agent asks for your approval
  all the time, and the permissions above don't prevent that. The deny rules stay in place as a
  safety net.
- Paste the block that `kb rules claude-code` prints into your project's CLAUDE.md. It tells the
  agent how to run `kb`, how to read the rules, and which two hold before anything else. It names no
  folders of yours, so it can be committed.
- The settings take effect right away, even in the session that wrote them. The CLAUDE.md block
  takes effect when a conversation starts, so after setting up, start a new conversation, or run
  `/clear`, before talking with the agent about anything you want recorded.
- Keep the knowledge base in git, with `off-record/`, `index.db` and `render/` left out: the first
  is outside the record by design, and the other two are rebuilt from the record whenever needed.
  `kb` does not commit for you; commit when it suits you, or ask your agent to.
- To keep everyone who writes to the knowledge base on the same version of Kent, pin it: `kb pin
  --set` writes `kent.pin` naming the version you are running, and `kb` then refuses to write under
  any other. Commit the pin; to move to a new version, set it again under that version. A pin is
  optional, whether you work alone or with others; what is required is that, when there is one,
  everyone who writes here obeys it.
- `kb` names you in the record by your git `user.email`. To use a different stable id, set it in
  `~/.kenttheclerk.json`: `{"user": "your-id"}`.
- In your own terminal, run `kb` from inside the knowledge base's folder, where it finds the record
  by itself: `kb <command>`. `serve` renders the knowledge base as pages
  to read, with statements, conversations and their links all browsable; `check` verifies the whole
  record against the rules; `where` tells you which folders `kb` is using.

## Where to go from here

Start a conversation with your agent about something you're deciding, and tell it whether you're on
the record. Ask it questions about how the knowledge base works as they come up; it has read this
page and the rules.
