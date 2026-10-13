package llm

import (
	"context"
	"encoding/json"
	"log/slog"

	"tollgate/core/db/repo"
	"tollgate/core/vault"
)

func LoadModels(ctx context.Context, store *repo.Store, v *vault.Vault) ([]*Model, error) {
	rows, err := store.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	creds := map[string]*repo.Credential{}
	secrets := map[string][]byte{}
	var out []*Model
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		m := &Model{ID: r.ID, Name: r.Name, Provider: r.Provider, UpstreamModel: r.UpstreamModel, APIBase: r.APIBase,
			Created: r.CreatedAt.Unix(),
			Prices: Prices{InputMicros: r.PriceInputMicros, OutputMicros: r.PriceOutputMicros,
				CacheReadMicros: r.PriceCacheReadMicros, CacheWriteMicros: r.PriceCacheWriteMicros}}
		if len(r.Extra) > 0 {
			if err := json.Unmarshal(r.Extra, &m.Extra); err != nil {
				slog.Warn("skipping model with invalid extra", "model", r.Name, "err", err)
				continue
			}
		}
		if r.CredentialID != nil {
			id := *r.CredentialID
			if _, ok := creds[id]; !ok {
				c, err := store.GetCredential(ctx, id)
				if err != nil {
					return nil, err
				}
				creds[id] = c
				if pt, err := v.Open(c.Name, c.Nonce, c.Ciphertext); err == nil {
					secrets[id] = pt
				} else {
					slog.Warn("credential failed to decrypt (wrong ENCRYPTION_KEY?)", "credential", c.Name)
				}
			}
			secret, ok := secrets[id]
			if !ok {
				slog.Warn("skipping model: credential unavailable", "model", r.Name)
				continue
			}
			m.Secret, m.CredentialKind = secret, creds[id].Kind
		}
		out = append(out, m)
	}
	return out, nil
}
