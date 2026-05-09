package server

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// =====================================================================
// Payments / transfers / quote / transactions
// =====================================================================

func (s *Server) CreatePayment(ctx context.Context, in *bankpb.CreatePaymentRequest) (*bankpb.PaymentResult, error) {
	r, err := s.Svc.CreatePayment(ctx, service.CreatePaymentInput{
		FromAccountID:   in.GetFromAccountId(),
		ToAccountNumber: in.GetToAccountNumber(),
		Amount:          in.GetAmount(),
		RecipientName:   in.GetRecipientName(),
		PaymentCode:     in.GetPaymentCode(),
		ReferenceNumber: in.GetReferenceNumber(),
		Purpose:         in.GetPurpose(),
		SaveRecipient:   in.GetSaveRecipient(),
	})
	if err != nil {
		return nil, err
	}
	return paymentResultToProto(r), nil
}

func (s *Server) CreateTransfer(ctx context.Context, in *bankpb.CreateTransferRequest) (*bankpb.PaymentResult, error) {
	r, err := s.Svc.CreateTransfer(ctx, service.CreateTransferInput{
		FromAccountID: in.GetFromAccountId(),
		ToAccountID:   in.GetToAccountId(),
		Amount:        in.GetAmount(),
		Purpose:       in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	return paymentResultToProto(r), nil
}

func (s *Server) QuoteExchange(ctx context.Context, in *bankpb.QuoteExchangeRequest) (*bankpb.QuoteExchangeResponse, error) {
	q, err := s.Svc.QuoteExchange(ctx,
		currencyFromProto(in.GetFrom()),
		currencyFromProto(in.GetTo()),
		in.GetAmount(),
		in.GetIncludeCommission())
	if err != nil {
		return nil, err
	}
	return &bankpb.QuoteExchangeResponse{
		FromAmount: q.FromAmount,
		ToAmount:   q.ToAmount,
		Rate:       q.Rate,
		Commission: q.Commission,
	}, nil
}

func (s *Server) ListTransactions(ctx context.Context, in *bankpb.ListTransactionsRequest) (*bankpb.ListTransactionsResponse, error) {
	ts, total, err := s.Svc.ListTransactions(ctx, domain.TransactionFilter{
		AccountID: in.GetAccountId(),
		OpKind:    in.GetOpKind(),
		Status:    in.GetStatus(),
	}, int(in.GetPage()), int(in.GetPageSize()))
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.Transaction, 0, len(ts))
	for _, t := range ts {
		out = append(out, transactionToProto(t))
	}
	page, pageSize := int(in.GetPage()), int(in.GetPageSize())
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	return &bankpb.ListTransactionsResponse{
		Transactions: out,
		Page:         int32(page),
		PageSize:     int32(pageSize),
		Total:        total,
	}, nil
}

// =====================================================================
// Recipients
// =====================================================================

func (s *Server) CreatePaymentRecipient(ctx context.Context, in *bankpb.CreatePaymentRecipientRequest) (*bankpb.PaymentRecipient, error) {
	r, err := s.Svc.CreatePaymentRecipient(ctx, in.GetName(), in.GetAccountNumber())
	if err != nil {
		return nil, err
	}
	return recipientToProto(r), nil
}

func (s *Server) ListPaymentRecipients(ctx context.Context, _ *bankpb.ListPaymentRecipientsRequest) (*bankpb.ListPaymentRecipientsResponse, error) {
	rs, err := s.Svc.ListPaymentRecipients(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.PaymentRecipient, 0, len(rs))
	for _, r := range rs {
		out = append(out, recipientToProto(r))
	}
	return &bankpb.ListPaymentRecipientsResponse{Recipients: out}, nil
}

func (s *Server) UpdatePaymentRecipient(ctx context.Context, in *bankpb.UpdatePaymentRecipientRequest) (*bankpb.PaymentRecipient, error) {
	r, err := s.Svc.UpdatePaymentRecipient(ctx, in.GetId(), in.GetName(), in.GetAccountNumber())
	if err != nil {
		return nil, err
	}
	return recipientToProto(r), nil
}

func (s *Server) DeletePaymentRecipient(ctx context.Context, in *bankpb.DeletePaymentRecipientRequest) (*emptypb.Empty, error) {
	if err := s.Svc.DeletePaymentRecipient(ctx, in.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// =====================================================================
// Cards
// =====================================================================

func (s *Server) CreateCard(ctx context.Context, in *bankpb.CreateCardRequest) (*bankpb.Card, error) {
	c, _, err := s.Svc.CreateCard(ctx, service.CreateCardInput{
		AccountID:          in.GetAccountId(),
		AuthorizedPersonID: in.GetAuthorizedPersonId(),
		Brand:              cardBrandFromProto(in.GetBrand()),
		Name:               in.GetName(),
		CardLimit:          in.GetCardLimit(),
	})
	if err != nil {
		return nil, err
	}
	// We don't surface the plaintext CVV in the proto response by
	// design — it lives in pkg/apperr-style "out-of-band" only when the
	// caller has a side channel (e.g., a signed in-band acknowledgement
	// in a future iteration). Tests retrieve it via the service layer
	// directly.
	return cardToProto(c), nil
}

func (s *Server) ListCards(ctx context.Context, in *bankpb.ListCardsRequest) (*bankpb.ListCardsResponse, error) {
	if in.GetAccountId() == "" {
		return nil, apperr.Validation("account_id is required")
	}
	cs, err := s.Svc.ListCards(ctx, in.GetAccountId())
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.Card, 0, len(cs))
	for _, c := range cs {
		out = append(out, cardToProto(c))
	}
	return &bankpb.ListCardsResponse{Cards: out}, nil
}

func (s *Server) SetCardStatus(ctx context.Context, in *bankpb.SetCardStatusRequest) (*bankpb.Card, error) {
	c, err := s.Svc.SetCardStatus(ctx, in.GetId(), cardStatusFromProto(in.GetStatus()))
	if err != nil {
		return nil, err
	}
	return cardToProto(c), nil
}

func (s *Server) UpdateCardLimit(ctx context.Context, in *bankpb.UpdateCardLimitRequest) (*bankpb.Card, error) {
	c, err := s.Svc.UpdateCardLimit(ctx, in.GetId(), in.GetCardLimit())
	if err != nil {
		return nil, err
	}
	return cardToProto(c), nil
}

// =====================================================================
// Authorized persons
// =====================================================================

func (s *Server) CreateAuthorizedPerson(ctx context.Context, in *bankpb.CreateAuthorizedPersonRequest) (*bankpb.AuthorizedPerson, error) {
	dob, err := parseDate(in.GetDateOfBirth())
	if err != nil {
		return nil, err
	}
	p, err := s.Svc.CreateAuthorizedPerson(ctx, service.CreateAuthorizedPersonInput{
		CompanyID:   in.GetCompanyId(),
		FirstName:   in.GetFirstName(),
		LastName:    in.GetLastName(),
		DateOfBirth: dob,
		Gender:      genderFromProto(in.GetGender()),
		Email:       in.GetEmail(),
		Phone:       in.GetPhone(),
		Address:     in.GetAddress(),
	})
	if err != nil {
		return nil, err
	}
	return authorizedPersonToProto(p), nil
}

func (s *Server) ListAuthorizedPersons(ctx context.Context, in *bankpb.ListAuthorizedPersonsRequest) (*bankpb.ListAuthorizedPersonsResponse, error) {
	ps, err := s.Svc.ListAuthorizedPersons(ctx, in.GetCompanyId())
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.AuthorizedPerson, 0, len(ps))
	for _, p := range ps {
		out = append(out, authorizedPersonToProto(p))
	}
	return &bankpb.ListAuthorizedPersonsResponse{AuthorizedPersons: out}, nil
}

// =====================================================================
// Conversions
// =====================================================================

func paymentResultToProto(r *domain.PaymentResult) *bankpb.PaymentResult {
	out := &bankpb.PaymentResult{
		OpId:   r.OpID,
		Status: txStatusToProto(r.Status),
	}
	for _, t := range r.Transactions {
		out.Transactions = append(out.Transactions, transactionToProto(t))
	}
	return out
}

func transactionToProto(t *domain.Transaction) *bankpb.Transaction {
	return &bankpb.Transaction{
		Id:                t.ID,
		OpId:              t.OpID,
		Kind:              txKindToProto(t.Kind),
		LegIndex:          int32(t.LegIndex),
		FromAccountId:     t.FromAccountID,
		ToAccountId:       t.ToAccountID,
		FromAmount:        t.FromAmount,
		ToAmount:          t.ToAmount,
		Rate:              t.Rate,
		RecipientName:     t.RecipientName,
		PaymentCode:       t.PaymentCode,
		ReferenceNumber:   t.ReferenceNumber,
		Purpose:           t.Purpose,
		InitiatorClientId: t.InitiatorClientID,
		Status:            txStatusToProto(t.Status),
		CreatedAt:         timestamppb.New(t.CreatedAt),
	}
}

func txKindToProto(k domain.TransactionKind) bankpb.TransactionKind {
	switch k {
	case domain.TxKindPayment:
		return bankpb.TransactionKind_TRANSACTION_KIND_PAYMENT
	case domain.TxKindTransfer:
		return bankpb.TransactionKind_TRANSACTION_KIND_TRANSFER
	case domain.TxKindExchange:
		return bankpb.TransactionKind_TRANSACTION_KIND_EXCHANGE
	case domain.TxKindFee:
		return bankpb.TransactionKind_TRANSACTION_KIND_FEE
	case domain.TxKindTrade:
		return bankpb.TransactionKind_TRANSACTION_KIND_TRADE
	case domain.TxKindTax:
		return bankpb.TransactionKind_TRANSACTION_KIND_TAX
	}
	return bankpb.TransactionKind_TRANSACTION_KIND_UNSPECIFIED
}

func txStatusToProto(s domain.TransactionStatus) bankpb.TransactionStatus {
	switch s {
	case domain.TxStatusRealized:
		return bankpb.TransactionStatus_TRANSACTION_STATUS_REALIZED
	case domain.TxStatusRejected:
		return bankpb.TransactionStatus_TRANSACTION_STATUS_REJECTED
	case domain.TxStatusProcessing:
		return bankpb.TransactionStatus_TRANSACTION_STATUS_PROCESSING
	}
	return bankpb.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
}

func recipientToProto(r *domain.PaymentRecipient) *bankpb.PaymentRecipient {
	return &bankpb.PaymentRecipient{
		Id:            r.ID,
		ClientId:      r.ClientID,
		Name:          r.Name,
		AccountNumber: r.AccountNumber,
		CreatedAt:     timestamppb.New(r.CreatedAt),
	}
}

func cardToProto(c *domain.Card) *bankpb.Card {
	return &bankpb.Card{
		Id:                 c.ID,
		Number:             c.Number,
		Brand:              cardBrandToProto(c.Brand),
		Name:               c.Name,
		AccountId:          c.AccountID,
		AuthorizedPersonId: c.AuthorizedPersonID,
		CardLimit:          c.CardLimit,
		Status:             cardStatusToProto(c.Status),
		ExpiresAt:          timestamppb.New(c.ExpiresAt),
		CreatedAt:          timestamppb.New(c.CreatedAt),
		UpdatedAt:          timestamppb.New(c.UpdatedAt),
	}
}

func cardBrandToProto(b domain.CardBrand) bankpb.CardBrand {
	switch b {
	case domain.BrandVisa:
		return bankpb.CardBrand_CARD_BRAND_VISA
	case domain.BrandMastercard:
		return bankpb.CardBrand_CARD_BRAND_MASTERCARD
	case domain.BrandDinacard:
		return bankpb.CardBrand_CARD_BRAND_DINACARD
	case domain.BrandAmex:
		return bankpb.CardBrand_CARD_BRAND_AMEX
	}
	return bankpb.CardBrand_CARD_BRAND_UNSPECIFIED
}

func cardBrandFromProto(b bankpb.CardBrand) domain.CardBrand {
	switch b {
	case bankpb.CardBrand_CARD_BRAND_VISA:
		return domain.BrandVisa
	case bankpb.CardBrand_CARD_BRAND_MASTERCARD:
		return domain.BrandMastercard
	case bankpb.CardBrand_CARD_BRAND_DINACARD:
		return domain.BrandDinacard
	case bankpb.CardBrand_CARD_BRAND_AMEX:
		return domain.BrandAmex
	}
	return ""
}

func cardStatusToProto(s domain.CardStatus) bankpb.CardStatus {
	switch s {
	case domain.CardActive:
		return bankpb.CardStatus_CARD_STATUS_ACTIVE
	case domain.CardBlocked:
		return bankpb.CardStatus_CARD_STATUS_BLOCKED
	case domain.CardDeactivated:
		return bankpb.CardStatus_CARD_STATUS_DEACTIVATED
	}
	return bankpb.CardStatus_CARD_STATUS_UNSPECIFIED
}

func cardStatusFromProto(s bankpb.CardStatus) domain.CardStatus {
	switch s {
	case bankpb.CardStatus_CARD_STATUS_ACTIVE:
		return domain.CardActive
	case bankpb.CardStatus_CARD_STATUS_BLOCKED:
		return domain.CardBlocked
	case bankpb.CardStatus_CARD_STATUS_DEACTIVATED:
		return domain.CardDeactivated
	}
	return ""
}

func authorizedPersonToProto(p *domain.AuthorizedPerson) *bankpb.AuthorizedPerson {
	return &bankpb.AuthorizedPerson{
		Id:          p.ID,
		CompanyId:   p.CompanyID,
		FirstName:   p.FirstName,
		LastName:    p.LastName,
		DateOfBirth: p.DateOfBirth.Format("2006-01-02"),
		Gender:      apGenderToProto(p.Gender),
		Email:       p.Email,
		Phone:       p.Phone,
		Address:     p.Address,
		CreatedAt:   timestamppb.New(p.CreatedAt),
		UpdatedAt:   timestamppb.New(p.UpdatedAt),
	}
}

func apGenderToProto(g domain.Gender) bankpb.Gender {
	switch g {
	case domain.GenderMale:
		return bankpb.Gender_GENDER_MALE
	case domain.GenderFemale:
		return bankpb.Gender_GENDER_FEMALE
	case domain.GenderOther:
		return bankpb.Gender_GENDER_OTHER
	}
	return bankpb.Gender_GENDER_UNSPECIFIED
}

func genderFromProto(g bankpb.Gender) domain.Gender {
	switch g {
	case bankpb.Gender_GENDER_MALE:
		return domain.GenderMale
	case bankpb.Gender_GENDER_FEMALE:
		return domain.GenderFemale
	case bankpb.Gender_GENDER_OTHER:
		return domain.GenderOther
	}
	return domain.GenderUnspecified
}

func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, apperr.Validation("date is required (YYYY-MM-DD)")
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, apperr.Validation("date must be YYYY-MM-DD")
	}
	return t, nil
}
