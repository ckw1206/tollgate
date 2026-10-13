-- A credential now carries its provider and endpoint, so models can leave api_base blank.
-- Resolution at request time: model.api_base ▸ credential.api_base ▸ provider default.
ALTER TABLE provider_credentials
    ADD COLUMN provider        provider,
    ADD COLUMN api_base        TEXT NOT NULL DEFAULT '',
    ADD COLUMN vertex_project  TEXT NOT NULL DEFAULT '',
    ADD COLUMN vertex_location TEXT NOT NULL DEFAULT '';

-- Provider: the one most models using the credential have; unused credentials go by kind.
UPDATE provider_credentials c SET provider = COALESCE(
    (SELECT m.provider FROM models m WHERE m.credential_id = c.id
     GROUP BY m.provider ORDER BY count(*) DESC, m.provider LIMIT 1),
    CASE c.kind WHEN 'gcp_service_account' THEN 'google'::provider ELSE 'openai'::provider END);

-- Vertex project/location: copied from a model that sets them (models keep theirs as overrides).
UPDATE provider_credentials c SET
    vertex_project = COALESCE((SELECT m.extra->>'vertex_project' FROM models m
        WHERE m.credential_id = c.id AND coalesce(m.extra->>'vertex_project', '') <> '' ORDER BY m.name LIMIT 1), ''),
    vertex_location = COALESCE((SELECT m.extra->>'vertex_location' FROM models m
        WHERE m.credential_id = c.id AND coalesce(m.extra->>'vertex_location', '') <> '' ORDER BY m.name LIMIT 1), '')
WHERE c.kind = 'gcp_service_account';

ALTER TABLE provider_credentials
    ALTER COLUMN provider SET NOT NULL,
    ADD CONSTRAINT credential_vertex_is_google CHECK (kind <> 'gcp_service_account' OR provider = 'google');
