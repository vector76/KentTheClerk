# Setting up Kent the Clerk in this project

A runbook for you, the agent. The human installed Kent and asked you to run `kb setup` and follow its
instructions. Kent the Clerk keeps a knowledge base grounded in conversations: what was decided, why,
and according to whom. This runbook connects the code project you are in to a knowledge base, new or
existing, and does the setup work, with the human guiding and approving every step. What `kb setup`
found in this project is printed after the runbook, with a list of what is left to do, in the order of
the steps below.

**Done** means:

- The code project's `.claude/settings.local.json` names the knowledge base, gives the agent the
  permissions Kent needs, and runs `kb session-start` when a conversation starts. That hook gives the
  agent Kent's instructions in every conversation.
- git ignores that file, and nothing the code project commits has changed.
- The knowledge base exists, the human has a user id, and they are in its user table.
- `kb setup --report` finds nothing left to do.

Ground rules for this conversation:

- **This setup conversation is not part of any record.** Do not snapshot it, attest anything, or record
  anything in it. The knowledge base starts with the human's first real conversation; the one act of
  setup is adding the human to its user table, which needs no conversation on the record.
- **Leave nothing in what the code project commits.** A team may want the codebase silent about Kent,
  so do not touch CLAUDE.md, README, `.gitignore` or any other tracked file of the code project, except
  to remove a block an earlier version of Kent had pasted into CLAUDE.md, with the human's agreement. If
  the human wants a note for their colleagues that the project has a knowledge base, that is theirs to
  write, in a README or another document for people, not CLAUDE.md. The one exception is a knowledge
  base the human chooses to keep inside the code repository: its own folder, and only that folder, is
  committed with the code.
- **Every change is approved.** Say what you are about to change before you change it. Many changes also
  bring up a Claude Code permission prompt, which is the human's approval: tell them up front to expect
  prompts for each `kb` command and for files outside the project until the settings exist, and one for
  the settings file itself, which Claude Code always asks about.
- **Back up a file before you edit it,** to the system's temporary folder (`$env:TEMP` on Windows, `/tmp`
  elsewhere) as `<name>.bak-<yyyyMMdd-HHmmss>`, and say where. Never leave a backup in the code
  repository, where git would see it and a `git add .` would commit it.
- **Never write inside a `.git/` folder.** Claude Code does not allow it, and it should not.
- **Never install or download a version of Kent yourself.** If one is missing, tell the human which, and
  they install it.

## 1. Explain, and settle every choice in one turn   [ STOP / ASK ]

Read the report below. Tell the human, in a few sentences, what Kent is for and what you found. If
**Left to do** says nothing, the project is set up: say so, ask whether they want to change the
project's settings (below), and go to step 3.

Otherwise ask all the questions that apply in one message, each with its options and a recommendation,
so the human can answer them together:

- **Which knowledge base.** A code project refers to at most one; several code projects may share one.
  If the report found a knowledge base inside the code repository, recommend that one: someone already
  keeps it there, and it travels with the code.
  - *New:* where it goes. Recommend a folder beside the code repository, named after the project, such
    as `<project>-kb`, usually its own git repository. It can sit inside the code repository instead,
    in a folder of its own used for nothing else, such as `kent` at the top; never mixed into a folder
    of other documents. Then the codebase is no longer silent about it: that folder, with its own
    `.gitignore` and a README for colleagues who don't have Kent yet, is committed with the code, and
    its record travels with the code's commits. Say so if they choose that.
  - *Existing:* where it is on this machine, for example one another project uses or one inside the
    code repository, or the URL of a git repository a colleague or the human has, to clone beside the
    code repository.
- **For a new one beside the code: keep it in git, and with a remote?** Git is optional but
  recommended: it is how a knowledge base is kept, shared and backed up. If yes: a first commit now?
  `kb` never changes anything in git; the agent does the git work, and what it does follows from where
  the knowledge base is, which the report states in one line:
  - *Not in git:* nothing commits it and nothing syncs it.
  - *Its own repository, no remote:* the agent commits it on every act; nothing syncs it.
  - *Its own repository with a remote:* the agent commits, pulls and pushes it on every act, as far as
    the remote can be reached. Without a connection it still acts and commits, and syncs once back.
    Copies that wrote on their own merge afterward. Adding a remote later is all it takes.
  - *Inside the code repository:* a person commits it, and it is pulled and pushed with the code.

  If the report says it is a git submodule, tell the human that Kent does not support a knowledge base
  in a submodule or subtree: it may work, but they are on their own with it.
- **For one inside the code repository: may the agent offer to commit?** The agent never commits it on
  its own initiative. By default it leaves committing to the human, who commits a change to the
  knowledge base together with the code that reflects it; or it can offer to commit after every
  attestation. This is a per-person setting, `KENT_OFFER_COMMIT`, below.
- **For a new one: pin it?** `kb pin --set` writes `kent.pin`, naming the version of Kent running now,
  and from then on `kb` refuses to write under any other version. It keeps everyone who writes here on
  the same version. It is optional, alone or in a team.
- **The human's id.** Each act names its author by a key, a short readable name such as `jamie` that
  never changes once used, and the knowledge base's user table says who each key is, by name and email
  (`kb rules people`). If the report says the human is recorded under a key in the table, tell them
  which, and ask only whether this knowledge base should know them by a different one. Otherwise ask
  for their name and email for the table, and how kb should know it is them: git's `user.email` in the
  knowledge base's folder, looked up in the table; `~/.kenttheclerk.json` holding `{"user": "<key>"}`,
  for every knowledge base on this machine; or an entry under `"folders"` in that file,
  `{"folders": {"<folder>": "<key>"}}`, for knowledge bases in that folder or below it. In a knowledge
  base whose acts already carry an id for them, which `kb user add` lists, that id is their key.
- **The project's settings,** kept in the settings file's `env`, per user and never committed:
  - `KENT_ASK_SESSION_MODE`: when the agent asks whether a conversation is on the record.
    `when-needed` (the default) asks when something worth recording comes up; `up-front` asks before
    anything else, for a project whose main work is the knowledge base.
  - `KENT_STATEMENTS_PER_TURN`: how many statements the agent presents for agreement per turn. `1` (the
    default) presents one at a time, which tends to get each one real attention.
  - `KENT_OFFER_COMMIT`, for a knowledge base inside the code repository only: `never` (the default)
    leaves committing to the human; `after-attestation` has the agent offer to commit after each one.
- **An old block in CLAUDE.md,** if the report found one: may you remove it? The hook now gives the agent
  those lines; carry its settings over into the ones above.

Also say which steps will bring up permission prompts. Wait for the answers.

## 2. Make the changes

Do what the human chose, in this order, saying what each step does as you go. Stop and ask again if
anything turns out different from what you described.

1. **The knowledge base.**
   - New: `kb setup --create <folder>` makes the folder with its `attested/` and `evidence/` folders,
     which is how `kb` finds a knowledge base. Run `kb pin --set` there if they asked for a pin.
   - If they chose git, or the folder is inside the code repository, which is always in git, set the
     folder up for git yourself: `git init` in it, unless the report says a repository already holds it;
     an empty `.gitkeep` file in `attested/` and in `evidence/`, since git keeps no empty folder and a
     clone must still be a knowledge base; and, in the knowledge base's own folder even when it is inside
     the code repository, a `.gitignore` holding:

     ```
     # off-record material: outside the record by design, never committed
     off-record/
     # rebuilt from the record whenever needed
     index.db
     index.db-journal
     render/
     ```

     and a `.gitattributes` holding:

     ```
     # entries added in separate copies are independent lines: a merge keeps both copies' new lines
     overrides.jsonl merge=union
     users-log.jsonl merge=union
     users.jsonl merge=union
     ```

   - Inside the code repository, also a `README.md` in the knowledge base's folder, for colleagues who
     check out the code without Kent. Fill in `<folder>` with the folder's path from the top of the
     code repository, and, if the human says where colleagues get Kent, put that in place of the
     sentence in brackets:

     ```
     # Knowledge base

     This folder is a knowledge base kept with Kent the Clerk: what was decided about this project in
     conversations with an AI agent, why, and who confirmed it. Nothing else belongs in this folder.

     - `attested/` holds the statements people confirmed, one file each: the place to start reading.
     - `agent/` holds working choices agents recorded, and `evidence/` the conversations they rest on.

     The files are plain text, but they are written only by Kent's `kb` command, never by hand.

     To ask the knowledge base questions, or add to it, from your own conversations with Claude Code:

     1. Install Kent the Clerk, so that `kb` is on your PATH. [Ask whoever set up this folder where to
        get it.]
     2. Open Claude Code at the top of this repository and ask it to run `kb setup` for the knowledge
        base in `<folder>`. Setup connects your copy of the code to it, in settings of your own that
        are never committed.
     3. Then ask, for example, "what did we decide about ..., and why?"
     ```

   - Then make the first commit if they asked for it, including `kent.pin` if there is one. For a
     knowledge base inside the code repository, commit only its folder, and only when the human says
     so: it goes into the code's history. A remote for one in its own repository is the human's to
     create; once it exists, `git -C <folder> remote add origin <url>` and a first push, with their
     agreement.
   - Existing, from a URL: `git clone <url> <folder>`, beside the code repository.
   - Either way, run `kb setup --report --kb <folder>` and read what it finds.
2. **The human's id, and their row in the user table.** Set the id if it needs setting. Then, if the
   report says they are not in the user table, add them, as the first act in a new knowledge base:
   `kb user add <name> --email <email>` prints the key it generates from the name; tell them, and run
   it again with `--key <key>`, theirs or the one they chose instead. The key must be the one their id
   names, and with git's `user.email` the email must be that one.
3. **The settings file.** `kb setup --report --kb <folder>` prints, under **Settings for this project**,
   the JSON Kent needs, with the paths already in the forms Claude Code wants, and what the current file
   lacks. Merge Kent's part into `.claude/settings.local.json`: add what is missing, keep everything else,
   and do not duplicate entries. Add the settings to `env` if they differ from the defaults. If
   `KENT_KB` names a different knowledge base, that is a change of knowledge base: ask first. Read the
   file back and make sure it is still valid JSON; Claude Code ignores a settings file it cannot parse.
   Tell the human what each part does:
   - `env.KENT_KB` names the knowledge base for this project only. It must not be a user-wide
     environment variable, which would point every project at the same knowledge base.
   - `permissions.additionalDirectories` and the `allow` rules let the agent read the knowledge base,
     run `kb`, and write drafts in `off-record/drafts/`. For a knowledge base in its own repository they
     also let it run git there, written `git -C <folder> …`, the form the session-start text tells the
     agent to use, since it commits on every act. If it moves into git later, run `kb setup` again.
   - The `deny` rules stop the agent from editing the record's files by hand, so only `kb` writes there.
   - `hooks.SessionStart` runs `kb session-start` whenever a conversation starts, is resumed, cleared or
     compacted. It prints Kent's instructions into the agent's context, from the version of Kent that
     runs, so nothing of Kent goes into CLAUDE.md.
4. **Keeping the settings file out of git,** if the report says git does not ignore it. Claude Code
   usually arranges this itself, with a line in the human's global git ignore file. Offer to add the line
   the report gives to that file, the path it names; it covers every project. If they would rather not,
   give them the line for `.git/info/exclude` to add themselves, since you may not write there.
5. **The old CLAUDE.md block,** if they agreed: remove that block and nothing else.

## 3. Check

Run `kb setup --report` and confirm **Left to do** is empty; deal with what it lists, asking as before.
Then run `kb session-start` and confirm it prints Kent's instructions, the knowledge base's folder and
what drives its git work: that is what the agent will see at the start of each conversation. Run `kb check` to confirm the
knowledge base reads cleanly.

## 4. Hand over

Tell the human:

- What was set up or changed, and where any backups are; they can delete them once satisfied.
- **Use Claude Code's auto mode.** Without it the agent asks for approval all the time, and the
  permissions do not prevent that. The deny rules stay in place as a safety net. Turning it on is theirs
  to do.
- **Start a new conversation** (or `/clear`) before talking about anything they want recorded: the
  session-start hook gives the agent Kent's instructions when a conversation starts. If Claude Code asks
  whether to trust the folder, the hook runs only once they accept.
- **How to begin:** start a conversation about something they are deciding, and say whether it is on
  the record. `kb rules introduction` explains how the knowledge base works; the agent reads it when they
  are new or ask why it works as it does.
- If the knowledge base is meant to be shared, the human creates its remote and pushes it. Colleagues
  then clone it and run `kb setup` in their own copy of the code project. One inside the code repository
  comes with the code: colleagues run `kb setup`, and its README tells those who don't have Kent yet.
- In their own terminal, `kb serve` in the knowledge base's folder renders it as pages to read.
- `kb` never changes anything in git. The agent does the git work in each conversation, as the
  knowledge base's place calls for: in its own repository it commits on every act, and with a remote
  it also pulls and pushes; inside the code repository it leaves commits to them, or offers, as
  `KENT_OFFER_COMMIT` says.
- After upgrading Kent, nothing in the project needs to change: the hook prints the instructions of
  whichever version runs. Running `kb setup` again at any time checks the setup and repairs it.

Stop here.
