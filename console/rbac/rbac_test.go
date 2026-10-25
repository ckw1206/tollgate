package rbac

import "testing"

func TestRoles(t *testing.T) {
	for _, p := range Permissions("admin") {
		if !Has("admin", p) {
			t.Fatalf("admin missing %s", p)
		}
	}
	if len(Permissions("admin")) != 20 {
		t.Fatalf("admin has %d permissions", len(Permissions("admin")))
	}
	if !Has("member", KeysWriteOwn) || !Has("member", PlaygroundUse) || Has("member", KeysWriteAll) || Has("member", UsersRead) {
		t.Fatal("member permissions wrong")
	}
	if Has("viewer", KeysWriteOwn) || Has("viewer", PlaygroundUse) || !Has("viewer", KeysReadOwn) || !Has("viewer", SpendReadOwn) {
		t.Fatal("viewer permissions wrong")
	}
	// metrics:read is admin-only: the admit-path numbers describe gateway
	// internals, so neither of the other roles may read them (#33).
	if !Has("admin", MetricsRead) || Has("member", MetricsRead) || Has("viewer", MetricsRead) {
		t.Fatal("metrics:read must be admin-only")
	}
	if Has("nobody", ModelsRead) {
		t.Fatal("unknown role has permissions")
	}
}
