package service

import (
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

func TestDeriveExternalCommitOpID_Stable(t *testing.T) {
	// Same inputs → same uuid across calls. Bank-side uses the same
	// formula; a divergence here desyncs ledger linkage.
	a := deriveExternalCommitOpID(333, "tx-1")
	b := deriveExternalCommitOpID(333, "tx-1")
	if a != b {
		t.Fatalf("expected stable derivation, got %q vs %q", a, b)
	}
	if c := deriveExternalCommitOpID(444, "tx-1"); c == a {
		t.Fatalf("expected different routing to yield different op_id, got %q", c)
	}
	if c := deriveExternalCommitOpID(333, "tx-2"); c == a {
		t.Fatalf("expected different tx_id to yield different op_id, got %q", c)
	}
}

func TestDeriveExternalExerciseOpID_NamespaceDisjoint(t *testing.T) {
	// The exercise-op namespace must NOT collide with the commit-op
	// namespace — partner-supplied exercise identifier could otherwise
	// alias an outbound saga's payment id.
	commit := deriveExternalCommitOpID(999, "abc")
	exercise := deriveExternalExerciseOpID("999", "abc")
	if commit == exercise {
		t.Fatalf("commit and exercise namespaces produced the same op_id: %q", commit)
	}
}

func TestExternalOTCTxIDDerivations_DistinctPerThread(t *testing.T) {
	if a := externalOTCAcceptTxID("thr-1"); a == externalOTCAcceptTxID("thr-2") {
		t.Fatalf("expected per-thread accept tx_id to differ")
	}
	if a := externalOTCExerciseTxID("ctr-1"); a == externalOTCExerciseTxID("ctr-2") {
		t.Fatalf("expected per-contract exercise tx_id to differ")
	}
	// Accept and exercise namespaces are disjoint — same input shouldn't
	// yield the same uuid for both.
	if externalOTCAcceptTxID("x") == externalOTCExerciseTxID("x") {
		t.Fatalf("accept and exercise namespaces collide")
	}
}

func TestAssertExternalOTCParty(t *testing.T) {
	thread := &domain.ExternalOTCThread{LocalUserID: "alice"}
	cases := []struct {
		name    string
		caller  auth.Principal
		wantErr bool
	}{
		{"local user", auth.Principal{UserID: "alice"}, false},
		{"admin override", auth.Principal{UserID: "elsewhere", Permissions: []string{"admin"}}, false},
		{"unrelated user", auth.Principal{UserID: "bob"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := assertExternalOTCParty(c.caller, thread)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v gotErr=%v", c.wantErr, err)
			}
		})
	}
}
