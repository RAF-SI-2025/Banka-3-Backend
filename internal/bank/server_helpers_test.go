package bank

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNormalizeTransactionStatus(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"all", ""},
		{"realized", "realized"},
		{"realizovano", "realized"},
		{"odbijeno", "rejected"},
		{"pending", "pending"},
		{"custom", "custom"},
	}

	for _, tt := range tests {
		if got := normalizeTransactionStatus(tt.in); got != tt.want {
			t.Fatalf("normalizeTransactionStatus(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDisplayTransactionStatus(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"realized", "Realizovano"},
		{"rejected", "Odbijeno"},
		{"pending", "U obradi"},
		{"something", "something"},
	}

	for _, tt := range tests {
		if got := displayTransactionStatus(tt.in); got != tt.want {
			t.Fatalf("displayTransactionStatus(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeSortAndType(t *testing.T) {
	if got := normalizeTransactionSortBy("id"); got != "tx.id" {
		t.Fatalf("unexpected sort by for id: %s", got)
	}
	if got := normalizeTransactionSortBy("type"); got != "tx.type" {
		t.Fatalf("unexpected sort by for type: %s", got)
	}
	if got := normalizeTransactionSortBy("from_account"); got != "tx.from_account" {
		t.Fatalf("unexpected sort by for from_account: %s", got)
	}
	if got := normalizeTransactionSortBy("to_account"); got != "tx.to_account" {
		t.Fatalf("unexpected sort by for to_account: %s", got)
	}
	if got := normalizeTransactionSortBy("amount"); got != "tx.start_amount" {
		t.Fatalf("unexpected sort by: %s", got)
	}
	if got := normalizeTransactionSortBy("end_amount"); got != "tx.end_amount" {
		t.Fatalf("unexpected sort by for end_amount: %s", got)
	}
	if got := normalizeTransactionSortBy("commission"); got != "tx.commission" {
		t.Fatalf("unexpected sort by for commission: %s", got)
	}
	if got := normalizeTransactionSortBy("status"); got != "tx.status" {
		t.Fatalf("unexpected sort by for status: %s", got)
	}
	if got := normalizeTransactionSortBy("timestamp"); got != "tx.timestamp" {
		t.Fatalf("unexpected sort by for timestamp: %s", got)
	}
	if got := normalizeTransactionSortBy("unknown"); got != "tx.timestamp" {
		t.Fatalf("unexpected default sort by: %s", got)
	}
	if got := normalizeTransactionSortOrder("asc"); got != "ASC" {
		t.Fatalf("unexpected sort order: %s", got)
	}
	if got := normalizeTransactionSortOrder("anything"); got != "DESC" {
		t.Fatalf("unexpected default sort order: %s", got)
	}
	if got := normalizeTransactionType("payment"); got != "payment" {
		t.Fatalf("unexpected transaction type: %s", got)
	}
	if got := normalizeTransactionType("transfer"); got != "transfer" {
		t.Fatalf("unexpected transaction type: %s", got)
	}
	if got := normalizeTransactionType("invalid"); got != "" {
		t.Fatalf("expected empty type, got %s", got)
	}
}

func TestNormalizeRecipientInput(t *testing.T) {
	name, account, err := normalizeRecipientInput(1, "  Ana  ", " 123 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Ana" || account != "123" {
		t.Fatalf("unexpected normalized values: %q %q", name, account)
	}

	_, _, err = normalizeRecipientInput(0, "A", "1")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for client, got %v", status.Code(err))
	}
	_, _, err = normalizeRecipientInput(1, "  ", "1")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for name, got %v", status.Code(err))
	}
	_, _, err = normalizeRecipientInput(1, "A", " ")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for account, got %v", status.Code(err))
	}
}

func TestValidationHelpers(t *testing.T) {
	if err := validateCreateCompanyInput(1, "A", 2, "B", 3); err != nil {
		t.Fatalf("expected valid create company input, got %v", err)
	}
	if status.Code(validateCreateCompanyInput(0, "A", 2, "B", 3)) != codes.InvalidArgument {
		t.Fatalf("expected invalid registered id")
	}
	if status.Code(validateUpdateCompanyInput(0, "A", "B", 1)) != codes.InvalidArgument {
		t.Fatalf("expected invalid update id")
	}

	future := time.Now().Add(time.Hour).Unix()
	if err := validateCreateAccountInput("A", 1, "RSD", "personal", "checking", 0, 0, 0, 1, future); err != nil {
		t.Fatalf("expected valid create account input, got %v", err)
	}
	if status.Code(validateCreateAccountInput("A", 1, "RSD", "personal", "checking", 0, -1, 0, 1, 0)) != codes.InvalidArgument {
		t.Fatalf("expected invalid daily limit")
	}
	if status.Code(validateCreateAccountInput("A", 1, "RSD", "personal", "checking", 0, 0, 0, 1, time.Now().Add(-time.Hour).Unix())) != codes.InvalidArgument {
		t.Fatalf("expected invalid valid_until")
	}
}

func TestMapToProtoHelpers(t *testing.T) {
	if mapCompanyToProto(nil) != nil {
		t.Fatalf("expected nil company mapping")
	}
	if mapCardToProto(nil) != nil {
		t.Fatalf("expected nil card mapping")
	}

	company := mapCompanyToProto(&Company{Id: 7, Name: "ACME"})
	if company == nil || company.Id != 7 || company.Name != "ACME" {
		t.Fatalf("unexpected company mapping: %+v", company)
	}

	now := time.Now().UTC().Truncate(time.Second)
	card := mapCardToProto(&Card{Id: 5, Number: "4111", Type: Debit, Brand: visa, Creation_date: now, Valid_until: now, Account_number: "1", Cvv: "123", Card_limit: 100, Status: Active})
	if card == nil || card.CardId != "5" || card.CardNumber != "4111" || card.Status != "active" {
		t.Fatalf("unexpected card mapping: %+v", card)
	}
}

func TestParseLoanType(t *testing.T) {
	tests := []string{"GOTOVINSKI", "STAMBENI", "AUTO", "REFINANSIRAJUCI", "STUDENTSKI"}
	for _, input := range tests {
		if _, err := parseLoanType(input); err != nil {
			t.Fatalf("parseLoanType(%q) failed: %v", input, err)
		}
	}

	_, err := parseLoanType("INVALID")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestModelTableNames(t *testing.T) {
	if (Currency{}).TableName() != "currencies" {
		t.Fatalf("unexpected table name")
	}
	if (Account{}).TableName() != "accounts" {
		t.Fatalf("unexpected table name")
	}
	if (ActivityCode{}).TableName() != "activity_codes" {
		t.Fatalf("unexpected table name")
	}
	if (Company{}).TableName() != "companies" {
		t.Fatalf("unexpected table name")
	}
	if (Card{}).TableName() != "cards" {
		t.Fatalf("unexpected table name")
	}
	if (AuthorizedParty{}).Table_name() != "authorized_party" {
		t.Fatalf("unexpected table name")
	}
	if (Payment{}).TableName() != "payments" {
		t.Fatalf("unexpected table name")
	}
	if (Transfer{}).TableName() != "transfers" {
		t.Fatalf("unexpected table name")
	}
	if (Loan{}).TableName() != "loans" {
		t.Fatalf("unexpected table name")
	}
	if (LoanInstallment{}).TableName() != "loan_installment" {
		t.Fatalf("unexpected table name")
	}
	if (LoanRequest{}).TableName() != "loan_request" {
		t.Fatalf("unexpected table name")
	}
	if (VerificationCode{}).TableName() != "verification_codes" {
		t.Fatalf("unexpected table name")
	}
	if (CardRequest{}).TableName() != "card_requests" {
		t.Fatalf("unexpected table name")
	}
	if (PaymentRecipient{}).TableName() != "payment_recipients" {
		t.Fatalf("unexpected table name")
	}
}
