-- server_icon is now "" (default icon) or an uploaded PNG/JPEG/WebP data URL, validated on
-- PUT /settings. No stored value predates the upload flow (the old field took an emoji or
-- URL), and one that fails the new validation would make every Settings save return 400,
-- so reset to the default icon.
UPDATE server_settings SET server_icon = '';
