// Package rbac defines console permissions. Roles live in code, not the DB.
package rbac

import "slices"

type Permission string

const (
	ModelsRead    Permission = "models:read"
	ModelsWrite   Permission = "models:write"
	KeysReadOwn   Permission = "keys:read_own"
	KeysWriteOwn  Permission = "keys:write_own"
	KeysReadAll   Permission = "keys:read_all"
	KeysWriteAll  Permission = "keys:write_all"
	UsersRead     Permission = "users:read"
	UsersWrite    Permission = "users:write"
	GroupsReadOwn Permission = "groups:read_own"
	GroupsReadAll Permission = "groups:read_all"
	GroupsWrite   Permission = "groups:write"
	InvitesRead   Permission = "invites:read"
	InvitesWrite  Permission = "invites:write"
	SpendReadOwn  Permission = "spend:read_own"
	SpendReadAll  Permission = "spend:read_all"
	AuditRead     Permission = "audit:read"
	SettingsRead  Permission = "settings:read"
	SettingsWrite Permission = "settings:write"
	PlaygroundUse Permission = "playground:use"
)

var viewer = []Permission{ModelsRead, KeysReadOwn, GroupsReadOwn, SpendReadOwn}

var roles = map[string][]Permission{
	"viewer": viewer,
	"member": append(slices.Clone(viewer), KeysWriteOwn, PlaygroundUse),
	"admin": {ModelsRead, ModelsWrite, KeysReadOwn, KeysWriteOwn, KeysReadAll, KeysWriteAll, UsersRead, UsersWrite,
		GroupsReadOwn, GroupsReadAll, GroupsWrite, InvitesRead, InvitesWrite, SpendReadOwn, SpendReadAll, AuditRead, SettingsRead, SettingsWrite, PlaygroundUse},
}

func Permissions(role string) []Permission { return roles[role] }

func Has(role string, p Permission) bool { return slices.Contains(roles[role], p) }

func ValidRole(role string) bool { _, ok := roles[role]; return ok }
