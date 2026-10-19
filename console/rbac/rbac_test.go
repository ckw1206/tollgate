package rbac

import "testing"

func TestRoles(t *testing.T) {
	for _, p := range Permissions("admin") {
		if !Has("admin", p) {
			t.Fatalf("admin missing %s", p)
		}
	}
	if len(Permissions("admin")) != 19 {
		t.Fatalf("admin has %d permissions", len(Permissions("admin")))
	}
	if !Has("member", KeysWriteOwn) || Has("member", KeysWriteAll) || Has("member", UsersRead) {
		t.Fatal("member permissions wrong")
	}
	if Has("viewer", KeysWriteOwn) || !Has("viewer", KeysReadOwn) || !Has("viewer", SpendReadOwn) {
		t.Fatal("viewer permissions wrong")
	}
	// Playground: admin and member may use it, the viewer never sees it.
	if !Has("admin", PlaygroundUse) || !Has("member", PlaygroundUse) || Has("viewer", PlaygroundUse) {
		t.Fatalf("playground:use wrong: admin=%v member=%v viewer=%v",
			Has("admin", PlaygroundUse), Has("member", PlaygroundUse), Has("viewer", PlaygroundUse))
	}
	if Has("nobody", ModelsRead) {
		t.Fatal("unknown role has permissions")
	}
}
