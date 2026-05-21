package trading

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type externalOTCLocalScope struct {
	UserID   string
	UserKind string
}

func (s *Server) resolveExternalOTCLocalScope(ctx context.Context) (*externalOTCLocalScope, error) {
	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.IsClient {
		return &externalOTCLocalScope{
			UserID:   strconv.FormatInt(caller.ClientID, 10),
			UserKind: "client",
		}, nil
	}
	if caller.IsEmployee {
		var employeeID int64
		if err := s.db.Table("employees").Select("id").Where("email = ?", caller.Email).Take(&employeeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.PermissionDenied, "employee not found")
			}
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		return &externalOTCLocalScope{
			UserID:   strconv.FormatInt(employeeID, 10),
			UserKind: "employee",
		}, nil
	}
	return nil, status.Error(codes.PermissionDenied, "unsupported caller")
}

func (s *Server) ListExternalOTCThreadsForCaller(ctx context.Context, statusFilter string) ([]ExternalOTCThreadRecord, error) {
	scope, err := s.resolveExternalOTCLocalScope(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.ListExternalOTCThreadsRecords(scope.UserID, statusFilter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return rows, nil
}

func (s *Server) GetExternalOTCThreadForCaller(ctx context.Context, threadID string) (*ExternalOTCThreadRecord, []ExternalOTCIterationRecord, *ExternalOTCContractRecord, error) {
	scope, err := s.resolveExternalOTCLocalScope(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	thread, err := s.GetExternalOTCThreadRecord(threadID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, status.Error(codes.NotFound, "external OTC thread not found")
		}
		return nil, nil, nil, status.Errorf(codes.Internal, "%v", err)
	}
	if thread.LocalUserID != scope.UserID {
		return nil, nil, nil, status.Error(codes.PermissionDenied, "thread does not belong to caller")
	}
	iterations, err := s.ListExternalOTCThreadIterationsRecord(threadID)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal, "%v", err)
	}

	var contract *ExternalOTCContractRecord
	contract, err = s.GetExternalOTCContractByThreadRecord(threadID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, status.Errorf(codes.Internal, "%v", err)
	}
	if contract != nil && contract.LocalUserID != scope.UserID {
		return nil, nil, nil, status.Error(codes.PermissionDenied, "contract does not belong to caller")
	}
	return thread, iterations, contract, nil
}

func (s *Server) ListExternalOTCContractsForCaller(ctx context.Context, statusFilter string) ([]ExternalOTCContractRecord, error) {
	scope, err := s.resolveExternalOTCLocalScope(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.ListExternalOTCContractsRecords(scope.UserID, statusFilter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return rows, nil
}

func externalOTCUserKindFromCaller(caller *bank.CallerIdentity) string {
	if caller != nil && caller.IsEmployee {
		return "employee"
	}
	return "client"
}

type CreateExternalOTCOfferInput struct {
	RemoteBankCode    string
	RemoteThreadID    string
	RemoteUserRef     string
	RemoteDisplayName string
	RemoteAccountRef  string
	BuyerAccountID    string
	SellerHoldingID   string
	SecurityTicker    string
	SecurityType      string
	Currency          string
	Quantity          int64
	PricePerUnit      string
	Premium           string
	SettlementDate    string
}

type CounterExternalOTCThreadInput struct {
	ThreadID       string
	Quantity       int64
	PricePerUnit   string
	Premium        string
	SettlementDate string
}

func (s *Server) CreateExternalOTCOfferForCaller(ctx context.Context, in CreateExternalOTCOfferInput) (*ExternalOTCThreadRecord, error) {
	scope, caller, err := s.resolveExternalOTCCaller(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.RemoteBankCode) == "" {
		return nil, status.Error(codes.InvalidArgument, "remote bank code is required")
	}
	if strings.TrimSpace(in.RemoteUserRef) == "" {
		return nil, status.Error(codes.InvalidArgument, "remote user ref is required")
	}
	if strings.TrimSpace(in.BuyerAccountID) == "" {
		return nil, status.Error(codes.InvalidArgument, "buyer account id is required")
	}
	if strings.TrimSpace(in.SecurityTicker) == "" {
		return nil, status.Error(codes.InvalidArgument, "security ticker is required")
	}
	if in.Quantity <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be positive")
	}
	settlement, err := parseExternalSettlementDate(in.SettlementDate)
	if err != nil {
		return nil, err
	}
	account, err := s.loadExternalBuyerAccount(ctx, caller, in.BuyerAccountID)
	if err != nil {
		return nil, err
	}
	securityID, err := s.resolveExternalSecurityID(in.SecurityTicker)
	if err != nil {
		return nil, err
	}

	thread := &ExternalOTCThreadRecord{
		ID:                 newExternalOTCID(),
		Direction:          "outbound",
		RemoteBankCode:     strings.TrimSpace(in.RemoteBankCode),
		RemoteThreadID:     strings.TrimSpace(in.RemoteThreadID),
		RemoteUserRef:      strings.TrimSpace(in.RemoteUserRef),
		RemoteDisplayName:  firstNonEmptyString(in.RemoteDisplayName, in.RemoteUserRef),
		RemoteAccountRef:   strings.TrimSpace(in.RemoteAccountRef),
		LocalUserID:        scope.UserID,
		LocalUserKind:      scope.UserKind,
		LocalAccountID:     strconv.FormatInt(account.Id, 10),
		LocalAccountNumber: account.Number,
		LocalRole:          "buyer",
		SecurityID:         securityID,
		SecurityTicker:     strings.ToUpper(strings.TrimSpace(in.SecurityTicker)),
		SellerHoldingID:    strings.TrimSpace(in.SellerHoldingID),
		Quantity:           in.Quantity,
		PricePerUnit:       normalizeMoneyString(in.PricePerUnit),
		Premium:            normalizeMoneyString(in.Premium),
		Currency:           strings.ToUpper(firstNonEmptyString(in.Currency, account.Currency)),
		SettlementDate:     settlement,
		ModifiedBySide:     "local",
		Status:             "open",
	}
	if err := s.CreateExternalOTCThreadRecord(thread); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if err := s.CreateExternalOTCIterationRecord(&ExternalOTCIterationRecord{
		ID:             newExternalOTCID(),
		ThreadID:       thread.ID,
		ProposedBySide: "local",
		Quantity:       in.Quantity,
		PricePerUnit:   thread.PricePerUnit,
		Premium:        thread.Premium,
		SettlementDate: settlement,
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return thread, nil
}

func (s *Server) CounterExternalOTCThreadForCaller(ctx context.Context, in CounterExternalOTCThreadInput) (*ExternalOTCThreadRecord, error) {
	thread, _, _, err := s.GetExternalOTCThreadForCaller(ctx, in.ThreadID)
	if err != nil {
		return nil, nilOrErr(err)
	}
	if thread.Status != "open" {
		return nil, status.Error(codes.FailedPrecondition, "thread is not open")
	}
	settlement, err := parseExternalSettlementDate(in.SettlementDate)
	if err != nil {
		return nil, err
	}
	if in.Quantity <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be positive")
	}
	if err := s.UpdateExternalOTCThreadTermsRecord(thread.ID, in.Quantity, normalizeMoneyString(in.PricePerUnit), normalizeMoneyString(in.Premium), settlement, "local"); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "external OTC thread not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if err := s.CreateExternalOTCIterationRecord(&ExternalOTCIterationRecord{
		ID:             newExternalOTCID(),
		ThreadID:       thread.ID,
		ProposedBySide: "local",
		Quantity:       in.Quantity,
		PricePerUnit:   normalizeMoneyString(in.PricePerUnit),
		Premium:        normalizeMoneyString(in.Premium),
		SettlementDate: settlement,
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return s.GetExternalOTCThreadRecord(thread.ID)
}

func (s *Server) WithdrawExternalOTCThreadForCaller(ctx context.Context, threadID string) (*ExternalOTCThreadRecord, error) {
	thread, _, _, err := s.GetExternalOTCThreadForCaller(ctx, threadID)
	if err != nil {
		return nil, nilOrErr(err)
	}
	if err := s.MarkExternalOTCThreadStatusRecord(thread.ID, "withdrawn"); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "external OTC thread not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return s.GetExternalOTCThreadRecord(thread.ID)
}

func (s *Server) AcceptExternalOTCThreadForCaller(ctx context.Context, threadID string) (*ExternalOTCThreadRecord, *ExternalOTCContractRecord, error) {
	thread, _, existingContract, err := s.GetExternalOTCThreadForCaller(ctx, threadID)
	if err != nil {
		return nil, nil, nilOrErr(err)
	}
	if thread.Status != "open" && thread.Status != "accepted" {
		return nil, nil, status.Error(codes.FailedPrecondition, "thread cannot be accepted")
	}
	if err := s.MarkExternalOTCThreadStatusRecord(thread.ID, "accepted"); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, status.Error(codes.NotFound, "external OTC thread not found")
		}
		return nil, nil, status.Errorf(codes.Internal, "%v", err)
	}
	if existingContract == nil {
		contract := &ExternalOTCContractRecord{
			ID:                 newExternalOTCID(),
			ThreadID:           thread.ID,
			Direction:          thread.Direction,
			RemoteBankCode:     thread.RemoteBankCode,
			RemoteThreadID:     thread.RemoteThreadID,
			RemoteUserRef:      thread.RemoteUserRef,
			RemoteDisplayName:  thread.RemoteDisplayName,
			RemoteAccountRef:   thread.RemoteAccountRef,
			LocalUserID:        thread.LocalUserID,
			LocalUserKind:      thread.LocalUserKind,
			LocalAccountID:     thread.LocalAccountID,
			LocalAccountNumber: thread.LocalAccountNumber,
			LocalRole:          thread.LocalRole,
			SecurityID:         thread.SecurityID,
			SecurityTicker:     thread.SecurityTicker,
			SellerHoldingID:    thread.SellerHoldingID,
			Quantity:           thread.Quantity,
			StrikePrice:        thread.PricePerUnit,
			PremiumPaid:        thread.Premium,
			Currency:           thread.Currency,
			SettlementDate:     thread.SettlementDate,
			AcceptedBySide:     "local",
			Status:             "active",
		}
		if err := s.CreateExternalOTCContractRecord(contract); err != nil {
			return nil, nil, status.Errorf(codes.Internal, "%v", err)
		}
		existingContract = contract
	}
	updatedThread, err := s.GetExternalOTCThreadRecord(thread.ID)
	if err != nil {
		return nil, nil, status.Errorf(codes.Internal, "%v", err)
	}
	return updatedThread, existingContract, nil
}

func (s *Server) MarkExternalOTCContractExercisedForCaller(ctx context.Context, contractID, exerciseOpID string, exercisedAt time.Time) (*ExternalOTCContractRecord, error) {
	scope, err := s.resolveExternalOTCLocalScope(ctx)
	if err != nil {
		return nil, err
	}
	contract, err := s.GetExternalOTCContractRecord(contractID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "external OTC contract not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if contract.LocalUserID != scope.UserID {
		return nil, status.Error(codes.PermissionDenied, "contract does not belong to caller")
	}
	if err := s.MarkExternalOTCContractExercisedRecord(contract.ID, strings.TrimSpace(exerciseOpID), exercisedAt); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "external OTC contract not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if err := s.MarkExternalOTCThreadStatusRecord(contract.ThreadID, "exercised"); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	updated, err := s.GetExternalOTCContractRecord(contract.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return updated, nil
}

func (s *Server) resolveExternalOTCCaller(ctx context.Context) (*externalOTCLocalScope, *bank.CallerIdentity, error) {
	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, nil, err
	}
	scope := &externalOTCLocalScope{UserKind: externalOTCUserKindFromCaller(caller)}
	if caller.IsClient {
		scope.UserID = strconv.FormatInt(caller.ClientID, 10)
		return scope, caller, nil
	}
	if caller.IsEmployee {
		var employeeID int64
		if err := s.db.Table("employees").Select("id").Where("email = ?", caller.Email).Take(&employeeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, status.Error(codes.PermissionDenied, "employee not found")
			}
			return nil, nil, status.Errorf(codes.Internal, "%v", err)
		}
		scope.UserID = strconv.FormatInt(employeeID, 10)
		return scope, caller, nil
	}
	return nil, nil, status.Error(codes.PermissionDenied, "unsupported caller")
}

func (s *Server) loadExternalBuyerAccount(ctx context.Context, caller *bank.CallerIdentity, accountID string) (*bank.Account, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(accountID), 10, 64)
	if err != nil || id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer account id must be numeric")
	}
	var acc bank.Account
	if err := s.db.Table("accounts").Where("id = ?", id).Take(&acc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "buyer account not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if err := s.bank.AuthorizeAccountAccess(ctx, &acc); err != nil {
		return nil, err
	}
	_ = caller
	return &acc, nil
}

func (s *Server) resolveExternalSecurityID(ticker string) (string, error) {
	var stock Stock
	if err := s.db.Where("ticker = ?", strings.ToUpper(strings.TrimSpace(ticker))).Take(&stock).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", status.Error(codes.NotFound, "security ticker not found")
		}
		return "", status.Errorf(codes.Internal, "%v", err)
	}
	return strconv.FormatInt(stock.ID, 10), nil
}

func parseExternalSettlementDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, status.Error(codes.InvalidArgument, "settlement date is required")
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Time{}, status.Error(codes.InvalidArgument, "invalid settlement date")
}

func normalizeMoneyString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "0"
	}
	return raw
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func newExternalOTCID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("ext-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func nilOrErr(err error) error { return err }
