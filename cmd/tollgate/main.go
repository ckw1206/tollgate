package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	consoleapi "tollgate/console/api"
	"tollgate/console/auth"
	"tollgate/core/api"
	"tollgate/core/cache"
	"tollgate/core/config"
	"tollgate/core/db"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
	"tollgate/core/llm/providers"
	"tollgate/core/spend"
	"tollgate/core/vault"
	"tollgate/core/webhook"
	"tollgate/core/worker"
	"tollgate/web"
)

// Set at build time: -ldflags "-X main.version=v1.2.3 -X main.commit=<sha>".
var version, commit = "dev", ""

// webhookDrainCap bounds the shutdown wait for the budget-webhook sender:
// delivery is best-effort, so after this much the sender's context is
// cancelled (in-flight request aborted, queue dropped) rather than drain a
// full queue against a dead endpoint (~1h at 256 × 3 retries × 5s).
const webhookDrainCap = 10 * time.Second

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	var err error
	if len(os.Args) > 2 && os.Args[1] == "admin" && os.Args[2] == "create" {
		err = adminCreate(os.Args[3:])
	} else if len(os.Args) > 1 && os.Args[1] != "serve" {
		err = fmt.Errorf("usage: tollgate [serve] | tollgate admin create --email E --name N")
	} else {
		err = serve()
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	v, err := vault.New(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		return err
	}
	c, err := cache.New(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Ping(ctx); err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	store := repo.New(d)
	var setup *consoleapi.SetupToken
	if n, err := store.CountUsers(ctx); err != nil {
		return err
	} else if n == 0 {
		// The one deliberate secret in the log: whoever can read it can create the first admin.
		var tok string
		setup, tok = consoleapi.NewSetupToken()
		slog.Warn("First-run setup: " + cfg.BaseURL + "/setup?token=" + tok)
	}

	httpClient := &http.Client{Timeout: 10 * time.Minute}
	reg := llm.NewRegistry(map[string]llm.Provider{
		"openai":    &providers.OpenAI{HTTP: httpClient},
		"anthropic": &providers.Anthropic{HTTP: httpClient},
		"google":    providers.NewGoogle(httpClient),
	})
	reload := func(ctx context.Context) error {
		ms, err := llm.LoadModels(ctx, store, v)
		if err != nil {
			return err
		}
		reg.Replace(ms)
		slog.Info("models loaded", "count", len(ms))
		return nil
	}
	if err := reload(ctx); err != nil {
		return err
	}

	// Budget-crossing webhook (#24). The sender is the last hop in the spend path:
	// Submit never blocks, every delivery failure is logged and dropped. The
	// client is built by webhook.NewWebhookClient (the same constructor the
	// test suite pins): its own timeout on top of the sender's per-attempt
	// timeout, a transport whose dialer refuses internal addresses (SSRF guard:
	// the URL is admin-configured), and no redirect following — a 3xx must fail
	// loudly, not be chased as a bodyless GET.
	webhookClient := webhook.NewWebhookClient(5 * time.Second)
	ws := webhook.NewSender(store, webhookClient)
	wsCtx, wsCancel := context.WithCancel(context.Background())
	defer wsCancel()
	go ws.Run(wsCtx)

	rec := spend.NewRecorder(store)
	recCtx, recCancel := context.WithCancel(context.Background())
	defer recCancel() // cancel after HTTP drains, so no handler records into a stopped recorder
	// Wired before Run starts so no flush can ever read OnCrossing nil-then-set
	// from another goroutine (the first flush can beat the assignment).
	rec.OnCrossing = func(c repo.BudgetCrossing) { ws.Submit(recCtx, c) }
	go rec.Run(recCtx)

	worker.Every(ctx, "budget_reset", time.Minute, func(ctx context.Context) error {
		_, err := store.ResetBudgets(ctx)
		return err
	})
	worker.Every(ctx, "spend_retention", 24*time.Hour, func(ctx context.Context) error {
		n, err := store.DeleteSpendBefore(ctx, time.Now().AddDate(0, 0, -cfg.SpendRetentionDays))
		if n > 0 {
			slog.Info("spend logs pruned", "rows", n)
		}
		return err
	})

	mux := http.NewServeMux()
	// One gateway server for /v1 and the console's playground: the playground
	// presents a key-less principal to the same ChatAs path the keys use.
	gateway := &api.Server{Store: store, Cache: c, Registry: reg, Record: rec.Record, Now: time.Now}
	gateway.Register(mux)
	(&consoleapi.Server{Store: store, Cache: c, Vault: v, Reload: reload, BaseURL: cfg.BaseURL, TrustedProxies: cfg.TrustedProxies,
		Version: version, Commit: commit, Setup: setup, Gateway: gateway,
		Sessions: &auth.Sessions{Cache: c, Store: store, Insecure: cfg.CookieInsecure, TTL: 24 * time.Hour}}).Register(mux)
	mux.Handle("GET /", web.Handler(web.Dist())) // console SPA; API routes above take precedence

	// No WriteTimeout: streams can run for minutes.
	srv := &http.Server{Addr: cfg.Addr, Handler: auth.CSRF(cfg.BaseURL, mux), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	slog.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("http shutdown", "err", err)
	}
	recCancel()  // after HTTP drains, so no handler records into a stopped recorder
	<-rec.Done() // the recorder's final flush (and its crossings) is done
	// The sender outlives the recorder: crossings from the final flush were
	// Submited after the signal, so the sender runs on its own context and is
	// shut down only after the recorder's Done, draining them before exit.
	// Drain is best-effort: cap the wait, then cancel the sender's context so
	// the in-flight request is aborted and the rest of the queue is dropped —
	// a dead endpoint must not hold a deploy (an hour per full queue) waiting
	// on a notification nobody bills against.
	ws.Close()
	select {
	case <-ws.Done():
	case <-time.After(webhookDrainCap):
		slog.Warn("webhook drain timed out; dropping queued crossings")
		wsCancel()
		<-ws.Done()
	}
	return nil
}

func adminCreate(args []string) error {
	fs := flag.NewFlagSet("admin create", flag.ExitOnError)
	email := fs.String("email", "", "admin email")
	name := fs.String("name", "", "display name")
	fs.Parse(args)
	if *email == "" || *name == "" {
		return errors.New("--email and --name are required")
	}
	pw := os.Getenv("TOLLGATE_ADMIN_PASSWORD")
	if pw == "" {
		fmt.Fprint(os.Stderr, "Password: ")
		b1, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "Confirm:  ")
		b2, _ := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if string(b1) != string(b2) {
			return errors.New("passwords do not match")
		}
		pw = string(b1)
	}
	if len(pw) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	ctx := context.Background()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	d, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		return err
	}
	s := repo.New(d)
	g, err := s.GetGroupByName(ctx, "default")
	if err != nil {
		return err
	}
	u, err := s.CreateUser(ctx, *email, strings.TrimSpace(*name), pw, "admin", g.ID)
	if errors.Is(err, repo.ErrConflict) {
		return fmt.Errorf("user %s already exists", *email)
	}
	if err != nil {
		return err
	}
	s.InsertAudit(ctx, repo.AuditEntry{ActorEmail: "cli", Action: "user.create", Resource: "users", ResourceID: u.ID})
	fmt.Fprintf(os.Stderr, "created admin %s (%s)\n", u.Email, u.ID)
	return nil
}
