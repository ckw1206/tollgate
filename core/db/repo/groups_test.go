package repo_test

import (
	"context"
	"slices"
	"testing"

	"tollgate/core/db/repo"
)

func TestUpdateGroup_NilModelsKeepsList(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	admin(t, s, g)
	in := repo.GroupInput{Name: "default", BudgetType: "unlimited", Models: &[]string{"a", "b"}}
	if got, err := s.UpdateGroup(ctx, g.ID, in); err != nil || !slices.Equal(got.Models, []string{"a", "b"}) || got.Members != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	in.Models, in.Name = nil, "renamed"
	if got, _ := s.UpdateGroup(ctx, g.ID, in); got.Name != "renamed" || !slices.Equal(got.Models, []string{"a", "b"}) {
		t.Fatalf("nil models should keep the list: %+v", got)
	}
	in.Models = &[]string{}
	if got, _ := s.UpdateGroup(ctx, g.ID, in); len(got.Models) != 0 {
		t.Fatalf("[] should mean all: %+v", got)
	}
	if c, _ := s.CreateGroup(ctx, repo.GroupInput{Name: "new", BudgetType: "unlimited"}); c.Models == nil || len(c.Models) != 0 || c.Members != 0 {
		t.Fatalf("create defaults: %+v", c)
	}
}

func TestAccess(t *testing.T) {
	for _, c := range []struct {
		group, extra []string
		model, want  string
	}{
		{nil, nil, "a", "group"},      // empty group = all
		{[]string{"a"}, nil, "b", ""}, // empty extras = none
		{[]string{"a"}, []string{"b"}, "b", "granted"},
		{[]string{"a"}, []string{"a"}, "a", "group"}, // group wins when both
	} {
		if got := repo.Access(c.group, c.extra, c.model); got != c.want {
			t.Errorf("Access(%v, %v, %q) = %q, want %q", c.group, c.extra, c.model, got, c.want)
		}
	}
}
