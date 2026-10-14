-- Model access by name, like api_keys.models: a group's allowed models (empty = every
-- enabled model, so upgrading changes nothing) and a user's extra grants (empty = none).
ALTER TABLE groups ADD COLUMN models TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE users ADD COLUMN extra_models TEXT[] NOT NULL DEFAULT '{}';
