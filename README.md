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
  word, the text the agent showed you, and that your agreement came after it, in your own turn. The
  one later difference it allows is a statement id that kb itself renamed, in a logged repair, when
  two copies gave the same id to different statements. If
  you want a change, say what it is; you can agree subject to that change, or ask to see the revised
  text first.
- **Statements come one at a time by default.** Reviewing several at once tends to mean the first
  gets all the attention. If you prefer more per turn, say so when setting up, or run `kb setup`
  again to change it.

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
the addition doesn't contradict it. Only you change what you said: someone else who sees it
differently says so in a statement of their own, linked to yours with a short note on how it changes
the reading, and yours is marked as having extra context. Both stay on the record, and a disagreement
stays a disagreement instead of being settled by whoever spoke last.

So you can't simply ask the agent to fix a statement's wording, not even a typo. The recorder can't
tell a typo fix from a change of meaning, so a typo is fixed the same way: you supersede your
statement with a corrected one. Keeping the history means anyone can see what was believed when, and
what replaced it.

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
the agent will ask whether to go on the record. (A setting you choose during `kb setup` can tell it
to ask at the start of every conversation instead.) If you reach a decision off the record, you bring it onto the record
by stating it yourself in an on-record conversation, for example: "As discussed earlier, I have
decided that ...". The agent may also offer to collect what's worth recording and propose a fresh
on-record conversation to capture it cleanly.

By default the record keeps what you could see. Your agent's hidden reasoning and tool output are
left out, because when you say "that's right", you are answering what was in front of you.

## Agent statements

Agents make many small choices while working for you, often ones that could reasonably go either
way. When such a choice is likely to come up again, the agent records it as an **agent statement**:
a working choice, in force until someone decides otherwise. Agent statements have ids of their own
(`A-7K2M` rather than `S-7K2M`), carry no attestations, and are never presented as something you
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

- **Install Kent the Clerk with its install script,** outside your repositories, the way you
  install a tool. This is strongly recommended. On Windows, in PowerShell:

  ```powershell
  Invoke-WebRequest https://github.com/vector76/KentTheClerk/releases/latest/download/install.ps1 -OutFile install.ps1
  powershell -ExecutionPolicy Bypass -File .\install.ps1
  ```

  On macOS or Linux:

  ```sh
  curl -fsSLO https://github.com/vector76/KentTheClerk/releases/latest/download/install.sh
  sh install.sh
  ```

  Each release carries its own install script, which installs that release and no other. These
  links always lead to the latest release's; to install an older version, download the script from
  that version's release instead, for example
  `https://github.com/vector76/KentTheClerk/releases/download/v0.9.0/install.ps1`. The script is
  short; read it before you run it. It holds its release's version and the checksums of that
  release's downloads, and checks what it downloads against them. It puts the version in
  `%LOCALAPPDATA%\Programs\KentTheClerk` on Windows, or `~/.local/share/kenttheclerk` on macOS and
  Linux, beside any versions already there, replaces the launcher `kb` only if the new one is newer,
  and adds the folder to your PATH the first time. `kb` runs the version your knowledge base is pinned to, or else
  the one `KENT_VERSION` names in your project's settings, or else the highest version installed;
  `kb --launcher` shows which, and why. This page, the rules, the setup runbook and what the
  session-start hook prints are built into each version, so nothing of Kent goes into your code repository or your knowledge base, and
  nothing else needs installing.

  Installing by hand is possible but not recommended, because each step the script takes is one you
  can get wrong. Download your platform's zip and `SHA256SUMS` from the project's GitHub releases,
  check the zip's SHA-256 against it, and unpack it somewhere temporary. Copy the
  `versions/<version>/` folder from inside it into `versions/` in your install folder, and copy
  `kb` (`kb.exe` on Windows) beside that only if there is none there yet, or if
  `kb --launcher version` reports the one in the zip as newer. Unpacking the zip straight into the
  install folder does not work: it nests everything in a folder named after the zip, and it
  overwrites the launcher whether or not the new one is newer. Add the install folder to your PATH.
  On macOS, executables downloaded through a browser are quarantined, so clear the flag on what you
  copied: `xattr -dr com.apple.quarantine <install folder>`.

  To build from a source checkout instead, run `build-dev.ps1 -Install` (or
  `build-dev.sh --install`) in the checkout; it installs a development build beside the launcher.
- **Then tell your agent: "Run kb setup and follow its instructions."** Open Claude Code in your
  code project, restarting it if it was open while you installed so that it finds `kb` on your PATH.
  `kb setup` gives the agent a runbook built into Kent, and the agent walks you through the setup,
  explaining each change and asking before it makes it:

  - **A knowledge base.** Either a new one, usually in its own folder beside the code repository
    and, if you choose, in a git repository of its own, or an existing one, such as one a colleague
    shares or one another of your projects uses. Several code projects can share one knowledge
    base; a code project uses at most one. A knowledge base can also live inside the code
    repository, in a folder of its own such as `kent`, so that it travels with the code; setup
    recognizes one that is already there, whatever its folder is called.
  - **Your project's `.claude/settings.local.json`.** It names the knowledge base for this project,
    gives the agent the permissions it needs, and holds a session-start hook that runs
    `kb session-start` whenever a conversation starts, which gives the agent Kent's instructions. The
    file is per user and never committed, so each person on a team keeps their own paths. Deny
    rules in it stop the agent from editing the record's files by hand, so only `kb` writes there.
  - **Settings of yours:** whether the agent asks about the session mode at the start of every
    conversation or only when something worth recording comes up, how many statements it
    presents per turn, and, for a knowledge base inside the code repository, whether it offers to
    commit after each attestation or leaves commits to you.

  Setup leaves nothing in what your code repository commits, not even a line in CLAUDE.md, so the
  codebase can stay silent about Kent. If you want colleagues to know the project has a knowledge
  base, mention it in a README or another document for people. A knowledge base kept inside the
  code repository is the exception: its folder is committed with the code, with its own
  `.gitignore` and a README telling colleagues who don't have Kent yet what the folder is and how to
  get set up. Run `kb setup` again whenever you
  like: it checks the setup and fixes what is wrong, with your agreement. Upgrading Kent needs no
  change in the project, since the hook prints the instructions of whichever version runs.
- **Work in Claude Code's auto mode.** It is required: without it, the agent asks for your approval
  all the time, and the permissions don't prevent that. The deny rules stay in place as a safety
  net.
- After setting up, start a new conversation, or run `/clear`, before talking with the agent about
  anything you want recorded: the hook gives the agent Kent's instructions when a conversation
  starts.
- Keeping the knowledge base in git is recommended, with `off-record/`, `index.db` and `render/`
  left out, as setup arranges: the first is outside the record by design, and the other two are
  rebuilt from the record whenever needed. `kb` never changes anything in git; your agent does the
  git work, and what it does follows from where the knowledge base is, which the session-start hook
  tells it. `kb rules record`, under Storage, has the table: in a repository of its own the agent
  commits on every act, and with a remote it also pulls and pushes; inside the code repository you
  commit it with the code it belongs to. Copies that wrote on their own merge afterward, and what
  kb cannot merge, such as two different changes to one statement's text, comes to you. A
  knowledge base in a git submodule or subtree is not supported.
- To keep everyone who writes to the knowledge base on the same version of Kent, pin it: `kb pin
  --set` writes `kent.pin` naming the version you are running, and `kb` then refuses to write under
  any other. Commit the pin; to move to a new version, set it again under that version. A pin is
  optional, whether you work alone or with others; what is required is that, when there is one,
  everyone who writes here obeys it. To run a particular installed version of Kent in one project
  without a pin, add `"KENT_VERSION": "<version>"` to `env` in its settings; a pin takes its place,
  and `kb` refuses if the two disagree.
- `kb` names you in the record by a key, a short readable name such as `jamie` that never changes,
  and the knowledge base's user table says who each key is, by name and email; a change of name or
  email changes your row, never the record. You add yourself to the table before your first act, as
  setup does, and `kb` refuses acts by anyone not in it. It finds your key from your git
  `user.email`, looked up in the table, or from `~/.kenttheclerk.json`: `{"user": "your-key"}`. To be
  known by a different key to some knowledge bases, add their folders: `{"user": "your-key",
  "folders": {"C:/work/client": "your-client-key"}}`. An entry applies to a knowledge base in that
  folder or below it, and the most specific one wins. Setup checks your key with you.
- In your own terminal, run `kb` from inside the knowledge base's folder, where it finds the record
  by itself: `kb <command>`. `serve` renders the knowledge base as pages
  to read, with statements, conversations and their links all browsable; `check` verifies the whole
  record against the rules; `where` tells you which folders `kb` is using.

## Where to go from here

Start a conversation with your agent about something you're deciding, and tell it whether you're on
the record. Ask it questions about how the knowledge base works as they come up; it has read this
page and the rules.
