-- Endpoint resolution is now credential.api_base ▸ provider default; a model's own api_base
-- counts only when it has no credential. Before 005 a credential had no endpoint, so a proxy or
-- self-hosted URL lived on its models: move it onto the credential when they all agree on one.
-- Vertex credentials have no endpoint field, so they're left alone.
UPDATE provider_credentials c SET api_base = agreed.base
FROM (SELECT m.credential_id AS id, min(m.api_base) AS base FROM models m
      WHERE m.api_base <> '' AND m.credential_id IS NOT NULL
      GROUP BY m.credential_id HAVING count(DISTINCT m.api_base) = 1) agreed
WHERE c.id = agreed.id AND c.api_base = '' AND c.kind = 'api_key';

-- A model's copy of its credential's endpoint is now redundant.
UPDATE models m SET api_base = '' FROM provider_credentials c
WHERE m.credential_id = c.id AND m.api_base <> '' AND m.api_base = c.api_base;
