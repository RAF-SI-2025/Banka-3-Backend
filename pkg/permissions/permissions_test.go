package permissions

import "testing"

func TestHas(t *testing.T) {
	holder := []string{Admin, EmployeeRead, ClientRead}
	if !Has(holder, Admin) {
		t.Error("Has should find Admin")
	}
	if Has(holder, EmployeeWrite) {
		t.Error("Has should not find EmployeeWrite")
	}
	if Has(nil, Admin) {
		t.Error("Has on nil should be false")
	}
	if Has([]string{}, Admin) {
		t.Error("Has on empty should be false")
	}
}

func TestHasAny(t *testing.T) {
	holder := []string{EmployeeRead}
	if !HasAny(holder, Admin, EmployeeRead) {
		t.Error("HasAny should match EmployeeRead")
	}
	if HasAny(holder, Admin, EmployeeWrite) {
		t.Error("HasAny should not match anything")
	}
	if HasAny(nil, Admin) {
		t.Error("HasAny on nil holder should be false")
	}
	if HasAny(holder /* no targets */) {
		t.Error("HasAny with no targets should be false")
	}
}

func TestHasAll(t *testing.T) {
	holder := []string{Admin, EmployeeRead, EmployeeWrite}
	if !HasAll(holder, Admin, EmployeeRead) {
		t.Error("HasAll should match a subset")
	}
	if HasAll(holder, Admin, ClientWrite) {
		t.Error("HasAll should fail on missing target")
	}
	if !HasAll(holder /* no targets */) {
		t.Error("HasAll with no targets should be vacuously true")
	}
}

func TestRoleBundlesContent(t *testing.T) {
	// Lock down the c1 role bundles so an accidental rename or reorder
	// surfaces as a test failure rather than a runtime change.
	cases := []struct {
		name   string
		bundle []string
		want   []string
	}{
		{"client-basic", RoleClientBasic, []string{ClientRead, AccountRead}},
		{"employee-basic", RoleEmployeeBasic, []string{EmployeeRead, ClientRead, AccountRead, CompanyRead}},
		{"employee-agent", RoleEmployeeAgent, []string{ClientRead, ClientWrite, AccountRead, AccountWrite, CompanyRead, CompanyWrite}},
		{"employee-supervisor", RoleEmployeeSupervisor, []string{EmployeeRead, ClientRead, ClientWrite, AccountRead, AccountWrite, CompanyRead, CompanyWrite}},
		{"employee-admin", RoleEmployeeAdmin, []string{Admin, EmployeeRead, EmployeeWrite, ClientRead, ClientWrite, CompanyRead, CompanyWrite, AccountRead, AccountWrite, ExchangeWrite, PermissionGrant}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.bundle) != len(tc.want) {
				t.Fatalf("len mismatch: got %v want %v", tc.bundle, tc.want)
			}
			for i := range tc.want {
				if tc.bundle[i] != tc.want[i] {
					t.Fatalf("[%d] got %q want %q", i, tc.bundle[i], tc.want[i])
				}
			}
		})
	}
}
