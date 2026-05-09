package service

import (
	"context"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
)

func TestRequireSupervisor(t *testing.T) {
	s := &Service{}

	cases := []struct {
		name        string
		ctx         context.Context
		wantAllowed bool
	}{
		{
			name:        "no principal",
			ctx:         context.Background(),
			wantAllowed: false,
		},
		{
			name: "client with trading perms",
			ctx: auth.WithPrincipal(context.Background(), auth.Principal{
				UserKind:    auth.KindClient,
				Permissions: []string{permissions.TradingClient},
			}),
			wantAllowed: false,
		},
		{
			name: "agent only",
			ctx: auth.WithPrincipal(context.Background(), auth.Principal{
				UserKind:    auth.KindEmployee,
				Permissions: []string{permissions.Actuary, permissions.ActuaryAgent},
			}),
			wantAllowed: false,
		},
		{
			name: "supervisor",
			ctx: auth.WithPrincipal(context.Background(), auth.Principal{
				UserKind:    auth.KindEmployee,
				Permissions: []string{permissions.Actuary, permissions.ActuarySupervisor},
			}),
			wantAllowed: true,
		},
		{
			name: "admin (implicit supervisor per spec p.38)",
			ctx: auth.WithPrincipal(context.Background(), auth.Principal{
				UserKind:    auth.KindEmployee,
				Permissions: []string{permissions.Admin},
			}),
			wantAllowed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.requireSupervisor(tc.ctx)
			got := err == nil
			if got != tc.wantAllowed {
				t.Fatalf("got allowed=%v err=%v, want allowed=%v", got, err, tc.wantAllowed)
			}
		})
	}
}

func TestValidateNonNegativeAmount(t *testing.T) {
	cases := []struct {
		in     string
		wantOK bool
	}{
		{"0", true},
		{"100000", true},
		{"100000.50", true},
		{"", true}, // money.Parse treats empty as 0
		{"-1", false},
		{"abc", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := validateNonNegativeAmount(tc.in)
			got := err == nil
			if got != tc.wantOK {
				t.Fatalf("got ok=%v err=%v, want ok=%v", got, err, tc.wantOK)
			}
		})
	}
}
