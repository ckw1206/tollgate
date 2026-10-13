package rbac

import "testing"

func TestRoles(t *testing.T) {
	for _, p := range Permissions("admin") {
		if !Has("admin", p) {
			t.Fatalf("admin missing %s", p)
		}
	}
	if len(Permissions("admin")) != 18 {
		t.Fatalf("admin has %d permissions", len(Permissions("admin")))
	}
	if !Has("member", KeysWriteOwn) || Has("member", KeysWriteAll) || Has("member", UsersRead) {
		t.Fatal("member permissions wrong")
	}
	if Has("viewer", KeysWriteOwn) || !Has("viewer", KeysReadOwn) || !Has("viewer", SpendReadOwn) {
		t.Fatal("viewer permissions wrong")
	}
	if Has("nobody", ModelsRead) {
		t.Fatal("unknown role has permissions")
	}
}
