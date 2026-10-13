-- When a credential's secret was last set; editing other fields leaves it alone.
ALTER TABLE provider_credentials ADD COLUMN secret_updated_at TIMESTAMPTZ;
UPDATE provider_credentials SET secret_updated_at = created_at;
ALTER TABLE provider_credentials ALTER COLUMN secret_updated_at SET NOT NULL, ALTER COLUMN secret_updated_at SET DEFAULT now();
