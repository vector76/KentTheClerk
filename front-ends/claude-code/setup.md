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
- The knowledge base exists, and the human has a user id.
- `kb setup --report` finds nothing left to do.

Ground rules for this conversation:

- **This setup conversation is not part of any record.** Do not snapshot it, attest anything, or record
  anything in it. The knowledge base starts with the human's first real conversation.
- **Leave nothing in what the code project commits.** A team may want the codebase silent about Kent,
  so do not touch CLAUDE.md, README, `.gitignore` or any other tracked file of the code project, except
  to remove a block an earlier version of Kent had pasted into CLAUDE.md, with the human's agreement. If
  the human wants a note for their colleagues that the project has a knowledge base, that is theirs to
  write, in a README or another document for people, not CLAUDE.md.
- **Every change is approved.** Say what you are about to change before you change it. Many changes also
  bring up a Claude Code permission prompt, which is the human's approval: tell them up front to expect
  prompts for each `kb` command and for files outside the project until the settings exist, and one for
  the settings file itself, which Claude Code always asks about.
- **Back up a file before you edit it,** next to itself as `<file>.bak-<yyyyMMdd-HHmmss>`, and say where.
- **Never write inside a `.git/` folder.** Claude Code does not allow it, and it should not.
- **Never install or download a version of Kent yourself.** If one is missing, tell the human which, and
  they install it.

## 1. Explain, and settle every choice in one turn   [ STOP / ASK ]

Read the report below. Tell the human, in a few sentences, what Kent is for and what you found. If
**Left to do** says nothing, the project is set up: say so, ask whether they want to change the
project's two settings (below), and go to step 3.

Otherwise ask all the questions that apply in one message, each with its options and a recommendation,
so the human can answer them together:

- **Which knowledge base.** A code project refers to at most one; several code projects may share one.
  - *New:* where it goes. Recommend a folder beside the code repository, named after the project, such
    as `<project>-kb`. It can sit inside the code repository instead, but then the codebase is no longer
    silent about it; say so if they choose that.
  - *Existing:* where it is on this machine, for example one another project uses, or the URL of a git
    repository a colleague or the human has, to clone beside the code repository.
- **For a new one: keep it in git?** Git is optional but recommended: it is how a knowledge base is
  kept, shared and backed up. If yes: a first commit now? `kb` never commits by itself.
- **For a new one: pin it?** `kb pin --set` writes `kent.pin`, naming the version of Kent running now,
  and from then on `kb` refuses to write under any other version. It keeps everyone who writes here on
  the same version. It is optional, alone or in a team.
- **The human's id,** if the report found none or it is not the one they want: git's `user.email` in the
  knowledge base's folder, or `~/.kenttheclerk.json` holding `{"user": "<id>"}`, which applies to every
  knowledge base on this machine. It should be stable, not a display name.
- **The project's two settings,** kept in the settings file's `env`, per user and never committed:
  - `KENT_ASK_SESSION_MODE`: when the agent asks whether a conversation is on the record.
    `when-needed` (the default) asks when something worth recording comes up; `up-front` asks before
    anything else, for a project whose main work is the knowledge base.
  - `KENT_STATEMENTS_PER_TURN`: how many statements the agent presents for agreement per turn. `1` (the
    default) presents one at a time, which tends to get each one real attention.
- **An old block in CLAUDE.md,** if the report found one: may you remove it? The hook now gives the agent
  those lines; carry its settings over into the two above.

Also say which steps will bring up permission prompts. Wait for the answers.

## 2. Make the changes

Do what the human chose, in this order, saying what each step does as you go. Stop and ask again if
anything turns out different from what you described.

1. **The knowledge base.**
   - New: `kb setup --create <folder>` makes the folder with its `attested/` and `evidence/` folders,
     which is how `kb` finds a knowledge base. Run `kb pin --set` there if they asked for a pin.
   - If they chose git, run git yourself in the knowledge base's folder: `git init`, unless the report
     says a repository already holds it; an empty `.gitkeep` file in `attested/` and in `evidence/`, since
     git keeps no empty folder and a clone must still be a knowledge base; and a `.gitignore` holding:

     ```
     # off-record material: outside the record by design, never committed
     off-record/
     # rebuilt from the record whenever needed
     index.db
     index.db-journal
     render/
     ```

     Then make the first commit if they asked for it, including `kent.pin` if there is one.
   - Existing, from a URL: `git clone <url> <folder>`, beside the code repository.
   - Either way, run `kb setup --report --kb <folder>` and read what it finds.
2. **The human's id,** if it needs setting.
3. **The settings file.** `kb setup --report --kb <folder>` prints, under **Settings for this project**,
   the JSON Kent needs, with the paths already in the forms Claude Code wants, and what the current file
   lacks. Merge Kent's part into `.claude/settings.local.json`: add what is missing, keep everything else,
   and do not duplicate entries. Add the two settings to `env` if they differ from the defaults. If
   `KENT_KB` names a different knowledge base, that is a change of knowledge base: ask first. Read the
   file back and make sure it is still valid JSON; Claude Code ignores a settings file it cannot parse.
   Tell the human what each part does:
   - `env.KENT_KB` names the knowledge base for this project only. It must not be a user-wide
     environment variable, which would point every project at the same knowledge base.
   - `permissions.additionalDirectories` and the `allow` rules let the agent read the knowledge base,
     run `kb`, and write drafts in `off-record/drafts/`.
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
Then run `kb session-start` and confirm it prints Kent's instructions and the knowledge base's folder:
that is what the agent will see at the start of each conversation. Run `kb check` to confirm the
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
  then clone it and run `kb setup` in their own copy of the code project.
- In their own terminal, `kb serve` in the knowledge base's folder renders it as pages to read.
- Committing the knowledge base is up to them; `kb` never commits.
- After upgrading Kent, nothing in the project needs to change: the hook prints the instructions of
  whichever version runs. Running `kb setup` again at any time checks the setup and repairs it.

Stop here.
