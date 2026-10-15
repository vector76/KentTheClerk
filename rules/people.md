# People

Every act in the record names its author by a key: a short readable name such as `jamie` or
`jeff-b`, which never changes once used. The user table, `users.jsonl` at the top of the knowledge
base, says who each key is now, by name and email. When someone's name or email changes, their row
changes and the record stays as it is. Keys are words rather than numbers so that the files stay easy
to read and search; wherever `kb` shows an author, it shows the name beside the key.

| What | Command |
|---|---|
| See who is in the table | `kb user` |
| Add a person | `kb user add <name> --email <email>`, then again with `--key <key>` |
| Change a person's name or email | `kb override user <key> --name <name> --email <email>` |
| Rename a key | `kb override rename <old> <new>` |

**Kent Rule 55.** The user table holds each person's key, name and email, and nothing else: no
nicknames, roles or notes. It is treated as fact, not testimony, so a row carries no evidential
weight and is not weighed. A nickname, or anything else about a person, can be attested on the
record instead. Only `kb` writes the table: `kb user add` adds a row and logs it in
`users-log.jsonl`, and `kb check` compares the table with its logs.

**Kent Rule 56.** A person is added to the user table before they can act, in every mode: `kb`
refuses any act by someone not in it, and says how to add them. Adding a person is an ordinary act,
done through conversation and logged. Run `kb user add` first without `--key`: `kb` prints the key it
generates from the name. Ask the human whether to use it or another, since the key never changes once
used, then run it again with the key they chose. In single-player and cooperative mode anyone may add
a person, and people add themselves; where there is an administrator, only the administrator adds
people, except the first person in a new knowledge base. Adding oneself is part of setting up a new
knowledge base. In a knowledge base made before the user table, each person adds themselves with the
key their acts already carry; until they do, `kb check` warns about those acts.

**Kent Rule 57.** `kb` finds who is acting from the key the user sets, with `--user`, `KB_USER` or
`~/.kenttheclerk.json`, where an entry under `folders` for the folder holding the knowledge base wins
over `user`, so one person can be known by different keys to different knowledge bases. Failing that,
it takes their git `user.email` in the knowledge base's folder and looks it up in the table. The key
is asserted, and every act records it as asserted.

**Kent Rule 58.** Changing the name or email in someone's row is an override (Kent Rules 50 to 52):
logged in the override log, made from an on-record session and checked twice. The key stays as it
was; renaming a key is the separate override `kb override rename`, which carries the row to the new
key.
