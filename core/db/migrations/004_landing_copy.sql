-- Landing page copy, editable in Settings. Empty means the console shows its default copy.
ALTER TABLE server_settings
    ADD COLUMN landing_headline TEXT NOT NULL DEFAULT '' CHECK (char_length(landing_headline) <= 80),
    ADD COLUMN landing_tagline  TEXT NOT NULL DEFAULT '' CHECK (char_length(landing_tagline) <= 160);
