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
		cw := int64(0)
		if r.ContextWindow != nil {
			cw = *r.ContextWindow
		}
		m := &Model{ID: r.ID, Name: r.Name, Provider: r.Provider, UpstreamModel: r.UpstreamModel, APIBase: r.APIBase,
			Created: r.CreatedAt.Unix(), ContextWindow: cw,
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
			c := creds[id]
			m.Secret, m.CredentialKind = secret, c.Kind
			// Endpoint: credential.api_base ▸ provider default (in the provider); a model's own
			// api_base counts only without a credential.
			// ponytail: legacy rows only. A model saved before credentials had an endpoint can
			// still carry its own api_base while its credential has none (migration 007 couldn't
			// pick one); keep it rather than send the secret to a host nobody configured.
			if c.APIBase != "" {
				m.APIBase = c.APIBase
			}
			if m.Extra.VertexProject == "" {
				m.Extra.VertexProject = c.VertexProject
			}
			if m.Extra.VertexLocation == "" {
				m.Extra.VertexLocation = c.VertexLocation
			}
		}
		out = append(out, m)
	}
	return out, nil
}
