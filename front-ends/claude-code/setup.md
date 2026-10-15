# Setting up Kent the Clerk in this project

A runbook for you, the agent. The human installed Kent and asked you to run `kb setup` and follow its
instructions. Kent the Clerk keeps a knowledge base grounded in conversations: what was decided, why,
and according to whom. This runbook connects the code project you are in to a knowledge base, new or
existing, and does the setup work, with the human guiding and approving every step. Follow the steps in
order, and stop at every `[ STOP / ASK ]` until the human answers. What `kb setup` found in this project
is printed after the runbook, with a list of what is left to do.

**Done** means:

- The code project's `.claude/settings.local.json` names the knowledge base, gives the agent the
  permissions Kent needs, and runs `kb session-start` when a conversation starts. That hook gives the
  agent Kent's instructions in every conversation.
- That file is kept out of git without any committed change.
- The knowledge base exists, is in git, and the human has a user id.
- `kb setup --report` finds nothing left to do.

Ground rules for this conversation:

- **This setup conversation is not part of any record.** Do not snapshot it, attest anything, or record
  anything in it. The knowledge base starts with the human's first real conversation.
- **Leave nothing in what the code project commits.** A team may want the codebase silent about Kent,
  so do not touch CLAUDE.md, README, `.gitignore` or any other tracked file of the code project. If the
  human wants a note for their colleagues that the project has a knowledge base, that is theirs to write,
  in a README or another document for humans, not CLAUDE.md.
- **Ask before every change, and say what it is.** Back up a file before you edit it, next to itself as
  `<file>.bak-<yyyyMMdd-HHmmss>`, and tell the human where.
- **Expect permission prompts.** Until the settings exist, Claude Code asks the human before each `kb`
  command and before writing the settings file. Tell the human that up front; it is expected.
- **Never install or download a version of Kent yourself.** If one is missing, tell the human which, and
  they install it.

## 1. Explain, and agree on the plan   [ STOP / ASK ]

Read the report below. In a few sentences, tell the human what Kent is for, what you found, and what you
propose to do, step by step. If **Left to do** says nothing, the project is set up: say so, offer step 6
(the project's settings) in case they want to change them, and go to step 8.

## 2. Choose the knowledge base   [ STOP / ASK ]

A code project refers to at most one knowledge base; several code projects may share one. If the report
names a knowledge base that is fine, confirm it with the human and go to step 4.

Otherwise ask which of these it is:

- **A new knowledge base.** Suggest a folder beside the code repository, named after the project, such as
  `<project>-kb`. It can sit inside the code repository instead, but then the codebase is no longer silent
  about it; say so if they choose that.
- **An existing knowledge base**, already on this machine (for example one another project uses), or in
  a git repository a colleague or the human has (ask for its URL, and clone it beside the code repository
  only with their agreement).

Then run `kb setup --report --kb <folder>` and read what it finds.

## 3. Create a new knowledge base   [ STOP / ASK ]

Only for a new one. Tell the human exactly what you will create, then, with their agreement:

1. Create the folder, with `attested/` and `evidence/` inside it, each holding an empty `.gitkeep` file
   so that a clone of the repository is a knowledge base too. `kb` finds a knowledge base by those two
   folders; the record fills them, and `agent/`, as the human works.
2. In the folder, run `git init`, and write a `.gitignore` holding exactly:

   ```
   # off-record material: outside the record by design, never committed
   off-record/
   # rebuilt from the record whenever needed
   index.db
   index.db-journal
   render/
   ```

3. Offer a first commit of those files. `kb` never commits; the human commits when it suits them, or asks
   the agent to.
4. Offer a pin, explaining it: `kb pin --set` writes `kent.pin`, naming the version of Kent running now,
   and from then on `kb` refuses to write under any other version. It keeps everyone who writes here on
   the same version. It is optional, alone or in a team. Set it only if the human says so.

If the knowledge base is meant to be shared, the human creates its remote and pushes it; tell them that
colleagues then clone it and run `kb setup` in their own copy of the code project.

## 4. The human's id

The report says who the human would be recorded as. If there is no id, or it is not the one they want,
offer the choices: git's `user.email` in the knowledge base's folder, or `~/.kenttheclerk.json` holding
`{"user": "<id>"}`, which applies to every knowledge base on this machine. The id should be stable, not
a display name. Ask before writing either.

## 5. Connect the code project   [ STOP / ASK ]

Run `kb setup --report --kb <folder>` again. Under **Settings for this project** it prints the JSON Kent
needs, with the paths already in the forms Claude Code wants, and lists what the current file lacks.
Tell the human what each part does:

- `env.KENT_KB` names the knowledge base for this project only. It must not be set as a user-wide
  environment variable, which would point every project at the same knowledge base.
- `permissions.additionalDirectories` and the `allow` rules let the agent read the knowledge base, run
  `kb`, and write drafts in `off-record/drafts/`.
- The `deny` rules stop the agent from editing the record's files by hand, so only `kb` writes there.
- `hooks.SessionStart` runs `kb session-start` whenever a conversation starts, is resumed, cleared or
  compacted. It prints Kent's instructions into the agent's context, from the version of Kent that runs,
  so nothing of Kent goes into CLAUDE.md.

With their agreement, back up the settings file if it exists, then merge Kent's part into it: add what is
missing, keep everything else as it is, and do not duplicate entries already there. If `KENT_KB` names a
different knowledge base, that is a change of knowledge base: ask first. Read the file back and make sure
it is still valid JSON; Claude Code ignores a settings file it cannot parse.

If the report says git does not ignore the settings file, add it to the repository's exclude file, the
path the report gives (usually `.git/info/exclude`). That file is local to this clone and never committed,
unlike `.gitignore`. Ask first.

If the report says a CLAUDE.md file still holds the knowledge base block from an earlier version of Kent,
offer to remove that block, and only that block: the hook now gives those lines. Leave the rest of the
file alone. This is a change to a tracked file, so it is the one exception to leaving tracked files
alone, and it needs the human's agreement like everything else. Carry its settings over in step 6.

## 6. The project's settings   [ STOP / ASK ]

Two settings, kept beside `KENT_KB` in the settings file's `env`, so they are per user and not
committed. Ask the human for each, explaining it:

- `KENT_ASK_SESSION_MODE`: when the agent asks whether a conversation is on the record.
  `when-needed` (the default) asks when something worth recording comes up; `up-front` asks before
  anything else, for a project whose main work is the knowledge base.
- `KENT_STATEMENTS_PER_TURN`: how many statements the agent presents for agreement per turn. `1` (the
  default) presents one at a time, which tends to get each one real attention.

Write only the ones that differ from the default, unless the human wants them written out.

## 7. Check

Run `kb setup --report` and confirm **Left to do** is empty; fix what it lists, asking as before. Then
run `kb session-start` and confirm it prints Kent's instructions and the knowledge base's folder. That is
what the agent will see at the start of each conversation. Run `kb check` to confirm the knowledge base
reads cleanly.

## 8. Hand over

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
- In their own terminal, `kb serve` in the knowledge base's folder renders it as pages to read.
- Committing the knowledge base is up to them; `kb` never commits.
- After upgrading Kent, nothing in the project needs to change: the hook prints the instructions of
  whichever version runs. Running `kb setup` again at any time checks the setup and repairs it.

Stop here.
