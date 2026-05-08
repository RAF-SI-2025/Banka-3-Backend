package service

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

func TestPermissionsForRole(t *testing.T) {
	cases := []struct {
		role string
		want []string
	}{
		{"admin", permissions.RoleEmployeeAdmin},
		{"ADMIN", permissions.RoleEmployeeAdmin},
		{"supervisor", permissions.RoleEmployeeSupervisor},
		{"agent", permissions.RoleEmployeeAgent},
		{"basic", permissions.RoleEmployeeBasic},
		{"", permissions.RoleEmployeeBasic},   // unknown → basic
		{"hax0r", permissions.RoleEmployeeBasic},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			got := permissionsForRole(tc.role)
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %v want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("[%d] got %q want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestPermissionsForRoleReturnsCopy(t *testing.T) {
	// Mutating the returned slice must not corrupt the role bundle.
	got := permissionsForRole("admin")
	got[0] = "tampered"
	if permissions.RoleEmployeeAdmin[0] == "tampered" {
		t.Fatal("permissionsForRole leaked the underlying role-bundle slice")
	}
}

func TestValidateCreateEmployee(t *testing.T) {
	full := CreateEmployeeInput{
		Email:       "marko@banka.local",
		Username:    "marko",
		FirstName:   "Marko",
		LastName:    "Marković",
		DateOfBirth: time.Date(1990, 5, 20, 0, 0, 0, 0, time.UTC),
		Gender:      domain.GenderMale,
		Phone:       "+381645555555",
		Address:     "Njegoševa 25",
		Position:    "Agent",
		Department:  "Trgovina",
	}
	if err := validateCreateEmployee(full); err != nil {
		t.Fatalf("happy path: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*CreateEmployeeInput)
		want string
	}{
		{"email", func(i *CreateEmployeeInput) { i.Email = "" }, "email"},
		{"username", func(i *CreateEmployeeInput) { i.Username = "" }, "username"},
		{"first name", func(i *CreateEmployeeInput) { i.FirstName = "" }, "first name"},
		{"last name", func(i *CreateEmployeeInput) { i.LastName = "" }, "last name"},
		{"dob", func(i *CreateEmployeeInput) { i.DateOfBirth = time.Time{} }, "date of birth"},
		{"gender", func(i *CreateEmployeeInput) { i.Gender = domain.GenderUnspecified }, "gender"},
		{"phone", func(i *CreateEmployeeInput) { i.Phone = "" }, "phone"},
		{"address", func(i *CreateEmployeeInput) { i.Address = "" }, "address"},
		{"position", func(i *CreateEmployeeInput) { i.Position = "" }, "position"},
		{"department", func(i *CreateEmployeeInput) { i.Department = "" }, "department"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := full
			tc.mut(&in)
			err := validateCreateEmployee(in)
			if err == nil {
				t.Fatalf("expected error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}

	formatCases := []struct {
		name string
		mut  func(*CreateEmployeeInput)
		want string
	}{
		{"missing @ in email", func(i *CreateEmployeeInput) { i.Email = "marko-banka.local" }, "email format"},
		{"phone with letters", func(i *CreateEmployeeInput) { i.Phone = "+38164abc" }, "phone format"},
		{"phone too short", func(i *CreateEmployeeInput) { i.Phone = "+1" }, "phone format"},
		{"dob in the future", func(i *CreateEmployeeInput) { i.DateOfBirth = time.Now().Add(48 * time.Hour) }, "must be in the past"},
	}
	for _, tc := range formatCases {
		t.Run(tc.name, func(t *testing.T) {
			in := full
			tc.mut(&in)
			err := validateCreateEmployee(in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestApplyEmployeePatch(t *testing.T) {
	now := time.Now()
	base := &domain.Employee{
		Email:       "old@x.local",
		Username:    "old",
		FirstName:   "Old",
		LastName:    "Name",
		DateOfBirth: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC),
		Gender:      domain.GenderMale,
		Phone:       "+1",
		Address:     "Old Street",
		Position:    "Old Position",
		Department:  "Old Dept",
		CreatedAt:   now,
	}

	t.Run("empty patch leaves everything untouched", func(t *testing.T) {
		e := *base
		applyEmployeePatch(&e, UpdateEmployeeInput{ID: "x"})
		if !reflect.DeepEqual(e, *base) {
			t.Fatalf("empty patch mutated employee: %+v", e)
		}
	})

	t.Run("editable fields applied with trimming and lowercase email", func(t *testing.T) {
		e := *base
		applyEmployeePatch(&e, UpdateEmployeeInput{
			ID:         "x",
			Email:      "  NEW@x.local  ",
			FirstName:  "  New  ",
			LastName:   "  Name2  ",
			Gender:     domain.GenderFemale,
			Phone:      "  +2  ",
			Address:    "  New Street  ",
			Position:   "  New Position  ",
			Department: "  New Dept  ",
		})
		want := domain.Employee{
			Email:       "new@x.local",
			Username:    "old", // unchanged — spec p.8 "Ne menja se"
			FirstName:   "New",
			LastName:    "Name2",
			DateOfBirth: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), // unchanged
			Gender:      domain.GenderFemale,
			Phone:       "+2",
			Address:     "New Street",
			Position:    "New Position",
			Department:  "New Dept",
			CreatedAt:   now,
		}
		if !reflect.DeepEqual(e, want) {
			t.Fatalf("patch result mismatch:\n got  %+v\n want %+v", e, want)
		}
	})

	t.Run("immutable fields ignored even when sent", func(t *testing.T) {
		// Spec p.8 marks Username and Datum rođenja as "Ne menja se".
		// Even if the proto carries them they must not be applied.
		e := *base
		applyEmployeePatch(&e, UpdateEmployeeInput{
			ID:          "x",
			Username:    "tampered",
			DateOfBirth: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		})
		if e.Username != base.Username {
			t.Fatalf("username changed despite being immutable: %q → %q", base.Username, e.Username)
		}
		if !e.DateOfBirth.Equal(base.DateOfBirth) {
			t.Fatalf("dob changed despite being immutable: %v → %v", base.DateOfBirth, e.DateOfBirth)
		}
	})

	t.Run("unspecified gender means no change", func(t *testing.T) {
		e := *base
		applyEmployeePatch(&e, UpdateEmployeeInput{ID: "x", Gender: domain.GenderUnspecified})
		if e.Gender != base.Gender {
			t.Fatalf("gender mutated: %v → %v", base.Gender, e.Gender)
		}
	})
}

func TestDiffEmployee(t *testing.T) {
	dob := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	before := &domain.Employee{
		Email: "a@x", Username: "a", FirstName: "A", LastName: "B",
		DateOfBirth: dob, Gender: domain.GenderMale,
		Phone: "+1", Address: "X1", Position: "P1", Department: "D1",
	}

	t.Run("no changes → empty slice", func(t *testing.T) {
		after := *before
		got := diffEmployee(before, &after)
		if len(got) != 0 {
			t.Fatalf("want empty diff, got %v", got)
		}
	})

	t.Run("each editable field produces one line", func(t *testing.T) {
		after := *before
		after.Email = "b@x"
		after.Username = "b"
		after.FirstName = "AA"
		after.LastName = "BB"
		after.DateOfBirth = dob.AddDate(1, 0, 0)
		after.Gender = domain.GenderFemale
		after.Phone = "+2"
		after.Address = "X2"
		after.Position = "P2"
		after.Department = "D2"
		got := diffEmployee(before, &after)
		if len(got) != 10 {
			t.Fatalf("want 10 diff lines, got %d: %v", len(got), got)
		}
		joined := strings.Join(got, "\n")
		for _, want := range []string{"Email", "Korisničko ime", "Ime", "Prezime", "Datum rođenja", "Pol", "Telefon", "Adresa", "Pozicija", "Departman"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing label %q in diff:\n%s", want, joined)
			}
		}
	})

	t.Run("dob diff uses YYYY-MM-DD", func(t *testing.T) {
		after := *before
		after.DateOfBirth = dob.AddDate(1, 0, 0)
		got := diffEmployee(before, &after)
		if len(got) != 1 || !strings.Contains(got[0], "1990-01-01 → 1991-01-01") {
			t.Fatalf("dob diff format wrong: %v", got)
		}
	})
}

func TestFormatLockMessage(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{15 * time.Minute, "Pokušajte ponovo za 15 min."},
		{14*time.Minute + 30*time.Second, "Pokušajte ponovo za 15 min."}, // rounded up
		{30 * time.Second, "Pokušajte ponovo za 1 min."},                 // floor 1
		{0, "Pokušajte ponovo za 1 min."},
	}
	for _, tc := range cases {
		t.Run(tc.in.String(), func(t *testing.T) {
			got := formatLockMessage(tc.in)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want substring %q", got, tc.want)
			}
			if !strings.HasPrefix(got, "Nalog je privremeno zaključan") {
				t.Fatalf("missing Serbian prefix: %q", got)
			}
		})
	}
}
