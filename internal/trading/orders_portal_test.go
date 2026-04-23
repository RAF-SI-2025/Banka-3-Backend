package trading

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequirePending(t *testing.T) {
	cases := []struct {
		name    string
		in      OrderStatus
		wantErr bool
	}{
		{"pending passes", StatusPending, false},
		{"approved rejects", StatusApproved, true},
		{"declined rejects", StatusDeclined, true},
		{"done rejects", StatusDone, true},
		{"cancelled rejects", StatusCancelled, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := requirePending(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				if status.Code(err) != codes.FailedPrecondition {
					t.Errorf("code = %s, want FailedPrecondition", status.Code(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRequireCancellable(t *testing.T) {
	cases := []struct {
		name    string
		in      OrderStatus
		wantErr bool
	}{
		{"pending cancellable", StatusPending, false},
		{"approved cancellable", StatusApproved, false},
		{"declined rejects", StatusDeclined, true},
		{"done rejects", StatusDone, true},
		{"cancelled rejects", StatusCancelled, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := requireCancellable(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				if status.Code(err) != codes.FailedPrecondition {
					t.Errorf("code = %s, want FailedPrecondition", status.Code(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseListStatus(t *testing.T) {
	cases := []struct {
		in      string
		want    OrderStatus
		wantErr bool
	}{
		{"", "", false},
		{"all", "", false},
		{"ALL", "", false},
		{"  all  ", "", false},
		{"pending", StatusPending, false},
		{"approved", StatusApproved, false},
		{"declined", StatusDeclined, false},
		{"done", StatusDone, false},
		{"cancelled", StatusCancelled, false},
		{"canceled", StatusCancelled, false},
		{"Pending", StatusPending, false},
		{"bogus", "", true},
		{"pend", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseListStatus(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", c.in)
				}
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("code = %s, want InvalidArgument", status.Code(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
