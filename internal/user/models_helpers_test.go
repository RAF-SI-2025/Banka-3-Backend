package user

import "testing"

func TestTableNameMappingsMatchDatabaseTables(t *testing.T) {
	if (Client{}).TableName() != "clients" {
		t.Fatalf("unexpected clients table name")
	}
	if (Employee{}).TableName() != "employees" {
		t.Fatalf("unexpected employees table name")
	}
	if (Permission{}).TableName() != "permissions" {
		t.Fatalf("unexpected permissions table name")
	}
	if (EmployeePermissions{}).TableName() != "employee_permissions" {
		t.Fatalf("unexpected employee_permissions table name")
	}
}

func TestBuildPasswordLinkValidatesBaseURL(t *testing.T) {
	if _, err := buildPasswordLink("", "abc"); err == nil {
		t.Fatalf("expected error for empty base URL")
	}
	if _, err := buildPasswordLink("://invalid", "abc"); err == nil {
		t.Fatalf("expected parse error for invalid URL")
	}

	link, err := buildPasswordLink("https://frontend/reset", "token123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if link != "https://frontend/reset?token=token123" {
		t.Fatalf("unexpected link: %s", link)
	}
}
