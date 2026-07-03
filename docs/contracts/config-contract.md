# Config contract

Default path: `~/.config/tg-drive-cli/config.toml` on Unix, `%APPDATA%\tg-drive-cli\config.toml` on Windows.

```toml
[telegram]
api_id = 123456
api_hash = "redacted in output"
phone = "+10000000000"

[storage]
db_path = "~/.local/share/tg-drive-cli/local_cache.db"
session_path = "~/.config/tg-drive-cli/session.json"

[caption]
safe_media_caption_utf16_units = 1024
safe_text_message_utf16_units = 4096
margin_utf16_units = 16
max_hashtags_in_caption = 32

[hash]
enabled = true
algorithm = "blake3"

[delete]
mode = "delete"

[limits]
free_upload_bytes = 2147483648
premium_upload_bytes = 4294967296

[locks]
ttl_seconds = 900

[[roots]]
local_path = "~/Pictures"
remote_path = "/"
channel_title = "Pictures [TD]"
strategy = "single"
```

Precedence:

```text
CLI flags > TD_* environment variables > config file > defaults
```
