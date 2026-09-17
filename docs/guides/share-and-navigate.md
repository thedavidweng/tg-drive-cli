# Share folders

How to give other people access to the channel. New posts use human-only
captions; path-derived `#td_` hashtags remain only as legacy compatibility
data.

Output blocks copied verbatim from real runs are unmarked (personal values
are masked). Blocks that depend on your own data are prefixed *Illustrative*.

---

## Share the whole drive

**Scenario:** someone should be able to browse and download everything in
the channel.

```sh
td share /
```

Captured from a real run (empty root — no files means no filter tag;
invite link masked):

```text
Channel: local [TD]
Invite: https://t.me/+<invite-hash>

Open the channel from the invite link; it contains everything shared here.
```

Anyone with the invite link joins as a channel member and can view and
download every file. That is the access model: sharing is all-or-nothing per
channel.

## Share one folder

**Scenario:** you want to point someone at a specific folder, not the whole
drive.

```sh
td share /2024
```

*Illustrative:*

```text
Channel: Pictures [TD]
Invite: https://t.me/+<invite-hash>
Legacy filter: #td_Pictures_<hash>_2024_<hash>

The filter is only useful for legacy posts that still carry path hashtags.
New uploads have clean human-only captions. Use `td ls` or `td tree` for
path-aware browsing.
```

The invite link still admits to the whole channel. It is not a permission
boundary.

## Legacy hashtag navigation

**Scenario:** you are in a Telegram client and want to jump to a folder
without `td`, and the folder contains legacy posts.

Legacy captions may carry cumulative path hashtags:

```text
#td_Pictures_<hash> #td_Pictures_<hash>_2024_<hash>
```

Tap a tag (or search for it) and the client may filter legacy messages down to
that folder. New uploads do not carry these tags. Two caveats:

- Modern clients may show **global** results across chats — choose the
  **current chat** tab.
- Public channels can scope tags as `#tag@username`; private channels cannot.

Chinese path segments become pinyin slugs; other non-ASCII scripts become
ASCII plus a short hash suffix. Hashtags are compatibility UX only — machine
reconstruction never relies on them (see [How td works](how-td-works.md)).

## Revoke access

**Scenario:** someone should no longer have access. `td` does not manage
members; use Telegram itself:

1. Open the channel in a Telegram client as owner.
2. Remove the member, or revoke the invite link in channel settings.

After revoking the old link, run `td share` again to mint a fresh one.

## Next steps

- [Organize files](organize-files.md) — restructure before you share
- [Troubleshoot](troubleshoot.md) — `td doctor` checks invite-link capability
