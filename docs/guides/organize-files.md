# Organize files

How to rename, move, and delete files in the virtual tree — including the
safety gates that stand between you and destructive operations.

Output blocks copied verbatim from real runs are unmarked (personal values
are masked). Blocks that depend on your own data are prefixed *Illustrative*.

---

## Preview before doing anything

**Scenario:** you want to see what an operation would touch. Every mutating
command has `--dry-run`.

```sh
td cp ~/hello.txt /hello.txt --dry-run
```

Captured from a real run (local path shortened):

```json
{"local":"/var/folders/…/hello.txt","policy":"fail","remote":"/hello.txt"}
```

`policy` shows the conflict behavior: `fail`, `replace`, `skip-existing`, or
`auto-rename`. Dry-run plans print as JSON even in text mode.

## Rename a file

**Scenario:** `beach.jpg` should be called `trip.jpg`. Renames are file-level;
directories cannot be moved or renamed.

Preview:

```sh
td mv /2024/beach.jpg /2024/trip.jpg --dry-run
```

*Illustrative:*

```json
{"to":"/2024/trip.jpg","would_move":"/2024/beach.jpg"}
```

Moving into an existing directory uses the source basename:
`td mv /2024/beach.jpg /Archive` lands at `/Archive/beach.jpg`.

Execute — remote writes require `--confirm`:

```sh
td mv /2024/beach.jpg /2024/trip.jpg --confirm
```

*Illustrative:*

```text
moved /2024/beach.jpg -> /2024/trip.jpg
```

Forgetting `--confirm` stops you safely. Captured from a real run:

```text
error: moving a remote file requires --confirm
```

(exit code 10, error code `ERR_CONFIRMATION_REQUIRED`)

**Next step:** delete something you no longer need.

## Delete a file

**Scenario:** remove a file from the drive. The default deletes the Telegram
message itself.

```sh
td rm /2024/trip.jpg --confirm
```

*Illustrative:*

```text
deleted /2024/trip.jpg
```

Captured from a real run — without `--confirm` nothing happens:

```text
error: deleting a remote file requires --confirm
```

### Tombstone instead of delete

**Scenario:** you want the index entry gone but the Telegram message kept
(readable by channel members). A tombstone redacts the caption instead of
deleting the message:

```sh
td rm /2024/trip.jpg --tombstone --confirm
```

*Illustrative:*

```text
deleted /2024/trip.jpg
```

If the manifest reply is too old to edit, the command fails with
`ERR_MESSAGE_NOT_EDITABLE`; rerun with `--allow-stale-manifest` to accept a
redacted main message plus a warning on stderr.

## Replace an uploaded file

**Scenario:** the local copy changed and the remote version should follow.
`--replace` overwrites; it counts as destructive, so it needs `--confirm`
too.

```sh
td cp ~/trip.jpg /2024/trip.jpg --replace --confirm
```

*Illustrative:*

```text
uploaded /2024/trip.jpg (2.4 MB)
```

Alternatives on conflict: `--skip-existing` keeps the remote file, and
`--auto-rename` uploads under a non-conflicting name.

## Upload several files as one album

**Scenario:** a batch of photos or videos should appear as a single grouped
post in the channel, not a pile of separate messages. Pass two or more
sources and a destination directory:

```sh
td cp ~/trip/day1.jpg ~/trip/day2.jpg ~/trip/day3.jpg /2024/trip/
```

*Illustrative:*

```text
uploaded 3 files in 1 album(s)
```

Native clients show the set as one swipeable album block. The first member
carries td's caption; each group of at most 10 members carries one
`td-album:v1` inventory reply, so `td scan --full` rebuilds everything after
a wipe. Sets larger than 10 files split into consecutive groups
automatically.

Rules worth knowing:

- The destination must be `/`, end with `/`, or name an existing remote
  directory.
- Conflict flags apply per file. `--skip-existing` may leave a single
  survivor — it then publishes as an ordinary single message, since Telegram
  albums need at least two members.
- `--replace` is not available in this form; replace existing files
  individually with single-path `td cp --replace`.
- Presentation flags (`--as`, `--thumb`, …) apply uniformly to every member;
  they are still rejected with `--recursive`, which groups each source
  directory's direct children into their own album.

## What moves and deletes cannot do

- Directory move/rename/delete are unsupported — file-level only.
- Empty directories live only in the local cache and vanish after a full scan.
- Moves across channels are rejected (`ERR_CROSS_CHANNEL_MOVE`).

## Next steps

- [Recover the index](recover-the-index.md) — undo mistakes after the fact by
  rescanning Telegram
- [Script with JSON](script-with-json.md) — parse dry-run plans and results
