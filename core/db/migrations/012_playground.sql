-- Playground: key-less spend rows + saved playground sessions (Task 1).
-- Playground spend is attributed to the user and group only: key_id is
-- nullable, and source marks the row's origin ('api' | 'playground').
ALTER TABLE spend_logs ALTER COLUMN key_id DROP NOT NULL;
ALTER TABLE spend_logs ADD COLUMN source TEXT NOT NULL DEFAULT 'api' CHECK (source IN ('api','playground'));

CREATE TABLE playground_sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  data JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX playground_sessions_user ON playground_sessions (user_id, updated_at DESC);
