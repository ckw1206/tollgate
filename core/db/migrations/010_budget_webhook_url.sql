-- Destination for budget-crossing webhook notifications (#24). Empty means
-- crossings are not delivered. Validated to http/https at write time (console).
ALTER TABLE server_settings ADD COLUMN budget_webhook_url TEXT NOT NULL DEFAULT '';
