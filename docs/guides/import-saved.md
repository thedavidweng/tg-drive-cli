# Import Saved Messages

`td import saved` re-uploads content from Telegram Saved Messages into the
bound drive channel. This makes `td` the durable owner of the bytes instead
of relying on a forwarded copy whose origin channel may later delete it.

## Preview and import

Preview the plan first:

```sh
td import saved --dry-run --photos-as document
```

Then run the import:

```sh
td import saved --photos-as document --confirm
```

The default destination is `/saved`. Saved Messages 2.0 sub-chats become
directories below it, for example `/saved/Trips/clip.mp4`. Text-only saved
messages become `.txt` notes. Albums stay native Telegram albums, split into
groups of at most ten members when necessary.

Pass message ids to import only selected items:

```sh
td import saved 101 102 --into /archive --photos-as document --confirm
```

## Photo handling

Telegram photo messages require an explicit choice:

- `--photos-as document` keeps the downloaded bytes and supports hash-based
  verification.
- `--photos-as photo` republishes a native photo card. Telegram may
  recompress it, so byte identity is not promised.

Interactive human runs can answer the prompt. JSON and non-interactive runs
must pass `--photos-as`.

## Duplicates and conflicts

Dedupe is enabled by default and uses the BLAKE3 content hash. A duplicate is
skipped and its caption is preserved in a `td-dupe:v1` discussion comment.
`--merge-captions` also appends that caption to the matched file, separated
by `---`. Use `--no-dedupe` to force a fresh upload.

Destination conflicts use the normal file policies:

```text
--replace
--skip-existing
--auto-rename
```

Without a policy, a path conflict fails. A rerun of an identical import is
recognized as a duplicate before that conflict is raised.

## Provenance and cleanup

Every successful imported file or album gets a `td-origin:v1` discussion
comment containing the Saved Messages id and any available forwarded origin
title, id, post id, and dates. These annotations are additive and are not
needed to reconstruct the file tree.

Sources stay in Saved Messages by default. `--delete-source --confirm`
deletes only items whose imported or duplicate drive message was verified.
Failed items are never deleted.

## Automation

Use JSON for a stable result envelope:

```sh
td --json import saved --dry-run --photos-as document
```

Use `--events` for NDJSON progress. It emits `import.item` envelopes as
items finish and one final `import` envelope.
