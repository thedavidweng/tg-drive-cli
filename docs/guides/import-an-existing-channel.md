# Import an existing channel

How to adopt messages that already exist in a Telegram channel — media you
uploaded by hand before using `td` — into the virtual file tree, without
re-uploading anything.

Output blocks copied verbatim from real runs are unmarked (personal values
are masked). Blocks that depend on your own data are prefixed *Illustrative*.

---

## Adopt a channel with history

**Scenario:** you initialized a root against an existing channel
(`td init <root> --bind-channel`) and `td ls /` shows nothing. The messages
are there; the index just does not know them yet.

Preview first — a dry run edits nothing on Telegram and writes no file rows:

```sh
td import --unmanaged --dry-run
```

Captured from a real run (channel without unmanaged media):

```text
import dry-run: 0 adopted, 0 skipped, 0 failed
```

On a channel with history the plan lists every message it would touch:

```sh
td import --unmanaged --dry-run
```

*Illustrative:*

```text
import dry-run: 3 adopted, 1 skipped, 0 failed
  video  msg 61  /videos/The Bet.mp4
  photo  msg 88  /photos/2024/06/team.jpg
  photo  msg 89  /photos/2024/06/offsite.jpg
  skip  msg 3  empty or service message
```

Paths are derived from captions; files without usable captions land under
`--into` (default `/`).

Run it for real. Adoption writes to Telegram (manifest replies) and to the
index, so it requires `--confirm`:

```sh
td import --unmanaged --confirm
```

*Illustrative:*

```text
import done: 3 adopted, 1 skipped, 0 failed
  video  msg 61  /videos/The Bet.mp4
  photo  msg 88  /photos/2024/06/team.jpg
  photo  msg 89  /photos/2024/06/offsite.jpg
  skip  msg 3  empty or service message
```

Verify:

```sh
td tree /
```

**Next step:** [share folders](share-and-navigate.md) or keep organizing —
[organize files](organize-files.md).

## Adopt one message

**Scenario:** one specific message should join the tree at a path you choose.

```sh
td import 61 --confirm "/videos/The Bet.mp4"
```

*Illustrative:*

```text
import done: 1 adopted, 0 skipped, 0 failed
  video  msg 61  /videos/The Bet.mp4
```

## Restore album captions

**Scenario:** the channel contains Telegram albums (grouped media). Albums
keep one human caption on the first item, so per-file metadata is missing and
machine reconstruction would be incomplete.

`--rewrite-captions` restores one human caption per album, deletes per-file
`td-manifest:v1` replies, and upserts one `td-album:v1` inventory reply per
group:

```sh
td import --unmanaged --rewrite-captions --confirm
```

*Illustrative:*

```text
import done: 2 captions restored, 5 replies deleted, 0 skipped, 0 failed
```

Caption edits on old messages can fail (`ERR_MESSAGE_NOT_EDITABLE`);
[td doctor](troubleshoot.md) reports whether edits currently work for your
channel.

## Compute hashes while adopting

**Scenario:** you want post-rebuild download verification. Without a stored
hash, rebuilt rows cannot prove content identity.

```sh
td import --unmanaged --hash --confirm
```

This downloads each adopted file once to compute its BLAKE3 hash, so it is
slower but makes future integrity checks possible.

## Next steps

- [Recover the index](recover-the-index.md) — full scan vs import: a scan
  re-reads managed messages, an import adopts unmanaged ones
- [Script with JSON](script-with-json.md) — machine-readable import plans
