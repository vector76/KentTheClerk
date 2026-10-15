# Developing Kent the Clerk

This repository is Kent the Clerk: the recorder `kb`, its launcher, and the rules and documents built
into it. The intent behind it lives in its own knowledge base, convkb, the folder `KENT_KB` names:
changes start there as decisions and are rendered here as code and prompts (S-0099). Sessions about
Kent run in this folder, the way a customer's sessions run in their project, and record into convkb.

## Knowledge base

Kent's instructions for working with the knowledge base reach each conversation through the
session-start hook in this project's `.claude/settings.local.json`, which runs `kb session-start`
(S-0129); that file also sets the project's settings: the session mode is asked about up front, and
statements are presented one at a time. If those instructions are not in your context, run
`kb setup`. Below, `<kb>/` stands for the knowledge base's folder.

One more setting for this project: output is priced in the human's audit time. Do not generate
documents nobody asked for; prefer a short restatement the human can attest over a long summary they
can only nod at.

## Working on Kent

- **Source.** The recorder is in `internal/kb/`, the launcher in `cmd/launcher/`. The rules are
  numbered in `rules/` and readable through `kb rules`; `<kb>/render-notes/kent-the-clerk.md` maps each
  rule to the statements it rests on, and is updated when a rule changes. The former Python recorder
  was retired at 0.8.0 and is gone.
- **Building.** After changing the source, rebuild and install with `./build-dev.ps1 -Install`. It
  installs a development build named after the version being worked on, such as `0.10.0-dev`, whose
  acts record the commit they were made under; a plain `go build` would stamp acts as if made under a
  release. `kb where` says which version ran. Run `go test ./...` and `kb check` before committing.
- **Versions.** Every attestation, agent statement, snapshot and ingest records the system it was made
  under (S-0030): `system_version`, set by hand as `defaultVersion` in `identity.go`, and `system_hash`,
  a content hash over the product's files (`kb version --files` lists them; this file is not among
  them). The version is bumped when work on the next version starts (S-0112).
- **Releasing.** Pushing a tag `vX.Y.Z` that matches `defaultVersion` runs the release workflow, which
  builds every platform and publishes the zips with the release's own install scripts (S-0116). Read
  the full message of every commit going into a release before tagging it. The repository is private,
  so the human downloads the release's `install.ps1` and Windows zip, and it is installed with
  `install.ps1 -ZipFile <zip>`.
- **Commits.** Commit straight to `main` here and to `master` in convkb (`git -C <kb>`), with no
  `Co-Authored-By` or other attribution lines; a `commit-msg` hook in both repositories refuses them.
- **User id.** It comes from `--user`, `KB_USER` or `~/.kenttheclerk.json`, where an entry under
  `folders` for the folder holding the knowledge base wins over `user` (S-0131), and otherwise from
  git's `user.email` in the knowledge base's folder (S-0123). Here it is `jamie`, set in
  `~/.kenttheclerk.json`.
