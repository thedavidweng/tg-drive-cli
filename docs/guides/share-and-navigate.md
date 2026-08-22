# Share folders

How to give other people access, and how the `#td_` hashtags map to your
folder tree inside native Telegram clients.

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
Filter: #td_Pictures_<hash>_2024_<hash>

Open the channel, then search or tap the filter tag. In clients that show global hashtag results, choose the current channel/chat tab.
```

The invite link still admits to the whole channel — the filter tag is a
navigation aid, not a permission.

## Navigate by hashtag in Telegram

**Scenario:** you are in a Telegram client and want to jump to a folder
without `td`.

Every directory level of every file caption carries a cumulative hashtag.
Illustrative structure:

```text
#td_Pictures_<hash> #td_Pictures_<hash>_2024_<hash>
```

Tap a tag (or search for it) and the client filters messages down to that
folder. Two caveats:

- Modern clients may show **global** results across chats — choose the
  **current chat** tab.
- Public channels can scope tags as `#tag@username`; private channels cannot.

Chinese path segments become pinyin slugs; other non-ASCII scripts become
ASCII plus a short hash suffix. Hashtags are UX sugar only — machine
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
