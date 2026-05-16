package service

import (
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// TestHoldingScope pins the GET /portfolio visibility rule. The
// regression it guards: a supervisor's bare /portfolio (no user_id)
// must scope to the supervisor's *own* employee-kind holdings. The
// pre-fix code left the filter empty for that case, so store.ListHoldings
// returned every user's rows (clients, funds, all employees); the FE
// rendered a Prodaj button on each and a SELL of someone else's row
// 500'd with "ne možete prodati više hartija nego što posedujete".
func TestHoldingScope(t *testing.T) {
	caller := auth.Principal{
		UserID:   "sup-1",
		UserKind: auth.KindEmployee,
	}
	client := auth.Principal{
		UserID:   "cli-1",
		UserKind: auth.KindClient,
	}

	cases := []struct {
		name         string
		p            auth.Principal
		supervisor   bool
		inUserID     string
		inUserKind   domain.UserKind
		wantUserID   string
		wantUserKind domain.UserKind
	}{
		{
			name:         "client always scoped to self",
			p:            client,
			supervisor:   false,
			inUserID:     "",
			wantUserID:   "cli-1",
			wantUserKind: domain.UserKind(auth.KindClient),
		},
		{
			name:         "agent always scoped to self even if it passes a user_id",
			p:            caller,
			supervisor:   false,
			inUserID:     "someone-else",
			inUserKind:   domain.UserKind(auth.KindClient),
			wantUserID:   "sup-1",
			wantUserKind: domain.UserKind(auth.KindEmployee),
		},
		{
			name:         "supervisor with NO user_id scopes to self (the bug)",
			p:            caller,
			supervisor:   true,
			inUserID:     "",
			wantUserID:   "sup-1",
			wantUserKind: domain.UserKind(auth.KindEmployee),
		},
		{
			name:         "supervisor with explicit user_id inspects that user",
			p:            caller,
			supervisor:   true,
			inUserID:     "cli-9",
			inUserKind:   domain.UserKind(auth.KindClient),
			wantUserID:   "cli-9",
			wantUserKind: domain.UserKind(auth.KindClient),
		},
		{
			name:         "supervisor with explicit user_id but no kind filters by id only",
			p:            caller,
			supervisor:   true,
			inUserID:     "fund-7",
			inUserKind:   "",
			wantUserID:   "fund-7",
			wantUserKind: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := holdingScope(tc.p, tc.supervisor, tc.inUserID, tc.inUserKind)
			if f.UserID != tc.wantUserID {
				t.Errorf("UserID = %q, want %q", f.UserID, tc.wantUserID)
			}
			if f.UserKind != tc.wantUserKind {
				t.Errorf("UserKind = %q, want %q", f.UserKind, tc.wantUserKind)
			}
			if f.SecurityID != "" {
				t.Errorf("SecurityID = %q, want empty (scope helper must not set it)", f.SecurityID)
			}
		})
	}
}

// TestHoldingScope_SupervisorPermissionBundle documents that the
// supervisor flag the helper takes is derived from the same permission
// set the service computes, so a real supervisor JWT (admin OR
// actuary.supervisor) takes the inspect path only with an explicit id.
func TestHoldingScope_SupervisorPermissionBundle(t *testing.T) {
	for _, perm := range []string{permissions.Admin, permissions.ActuarySupervisor} {
		isSup := permissions.HasAny([]string{perm}, permissions.Admin, permissions.ActuarySupervisor)
		if !isSup {
			t.Fatalf("%q should count as supervisor", perm)
		}
		f := holdingScope(auth.Principal{UserID: "me", UserKind: auth.KindEmployee}, isSup, "", "")
		if f.UserID != "me" {
			t.Errorf("%q with no user_id: UserID = %q, want self", perm, f.UserID)
		}
	}
	if permissions.HasAny([]string{permissions.ActuaryAgent}, permissions.Admin, permissions.ActuarySupervisor) {
		t.Errorf("actuary.agent must not count as supervisor")
	}
}
