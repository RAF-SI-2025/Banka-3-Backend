package trading

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type externalOTCThreadDTO struct {
	ID                 string `json:"id"`
	Direction          string `json:"direction"`
	RemoteBankCode     string `json:"remoteBankCode"`
	RemoteThreadID     string `json:"remoteThreadId"`
	RemoteUserRef      string `json:"remoteUserRef"`
	RemoteDisplayName  string `json:"remoteDisplayName"`
	RemoteAccountRef   string `json:"remoteAccountRef"`
	LocalUserID        string `json:"localUserId"`
	LocalUserKind      string `json:"localUserKind"`
	LocalAccountID     string `json:"localAccountId"`
	LocalAccountNumber string `json:"localAccountNumber"`
	LocalRole          string `json:"localRole"`
	SecurityID         string `json:"securityId"`
	SecurityTicker     string `json:"securityTicker"`
	SellerHoldingID    string `json:"sellerHoldingId"`
	Quantity           int64  `json:"quantity"`
	PricePerUnit       string `json:"pricePerUnit"`
	Premium            string `json:"premium"`
	Currency           string `json:"currency"`
	SettlementDate     string `json:"settlementDate"`
	ModifiedBySide     string `json:"modifiedBySide"`
	Status             string `json:"status"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

type externalOTCIterationDTO struct {
	ID             string `json:"id"`
	ThreadID       string `json:"threadId"`
	ProposedBySide string `json:"proposedBySide"`
	Quantity       int64  `json:"quantity"`
	PricePerUnit   string `json:"pricePerUnit"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlementDate"`
	CreatedAt      string `json:"createdAt"`
}

type externalOTCContractDTO struct {
	ID                 string `json:"id"`
	ThreadID           string `json:"threadId"`
	Direction          string `json:"direction"`
	RemoteBankCode     string `json:"remoteBankCode"`
	RemoteThreadID     string `json:"remoteThreadId"`
	RemoteUserRef      string `json:"remoteUserRef"`
	RemoteDisplayName  string `json:"remoteDisplayName"`
	RemoteAccountRef   string `json:"remoteAccountRef"`
	LocalUserID        string `json:"localUserId"`
	LocalUserKind      string `json:"localUserKind"`
	LocalAccountID     string `json:"localAccountId"`
	LocalAccountNumber string `json:"localAccountNumber"`
	LocalRole          string `json:"localRole"`
	SecurityID         string `json:"securityId"`
	SecurityTicker     string `json:"securityTicker"`
	SellerHoldingID    string `json:"sellerHoldingId"`
	Quantity           int64  `json:"quantity"`
	StrikePrice        string `json:"strikePrice"`
	PremiumPaid        string `json:"premiumPaid"`
	Currency           string `json:"currency"`
	SettlementDate     string `json:"settlementDate"`
	AcceptedBySide     string `json:"acceptedBySide"`
	Status             string `json:"status"`
	PremiumOpID        string `json:"premiumOpId"`
	ExerciseOpID       string `json:"exerciseOpId"`
	ExercisedAt        string `json:"exercisedAt,omitempty"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

type createExternalOTCOfferHTTPRequest struct {
	RemoteBankCode    string `json:"remoteBankCode"`
	RemoteThreadID    string `json:"remoteThreadId"`
	RemoteUserRef     string `json:"remoteUserRef"`
	RemoteDisplayName string `json:"remoteDisplayName"`
	RemoteAccountRef  string `json:"remoteAccountRef"`
	BuyerAccountID    string `json:"buyerAccountId"`
	SellerHoldingID   string `json:"sellerHoldingId"`
	SecurityTicker    string `json:"securityTicker"`
	SecurityType      string `json:"securityType"`
	Currency          string `json:"currency"`
	Quantity          int64  `json:"quantity"`
	PricePerUnit      string `json:"pricePerUnit"`
	Premium           string `json:"premium"`
	SettlementDate    string `json:"settlementDate"`
}

type counterExternalOTCOfferHTTPRequest struct {
	Quantity       int64  `json:"quantity"`
	PricePerUnit   string `json:"pricePerUnit"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlementDate"`
}

type exerciseExternalOTCContractHTTPRequest struct {
	ExerciseOpID string `json:"exerciseOpId"`
}

func RegisterExternalOTCInternalHTTP(mux *http.ServeMux, srv *Server, apiKey string) {
	mux.HandleFunc("GET /internal/api/v1/otc/external/threads", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := externalOTCInternalContext(r, apiKey)
		if !ok {
			writeExternalOTCJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		rows, err := srv.ListExternalOTCThreadsForCaller(ctx, r.URL.Query().Get("status"))
		if err != nil {
			writeExternalOTCStatusError(w, err)
			return
		}
		out := make([]externalOTCThreadDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, externalOTCThreadToDTO(row))
		}
		writeExternalOTCJSON(w, http.StatusOK, map[string]any{"threads": out})
	})

	mux.HandleFunc("GET /internal/api/v1/otc/external/threads/{threadId}", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := externalOTCInternalContext(r, apiKey)
		if !ok {
			writeExternalOTCJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		thread, iterations, contract, err := srv.GetExternalOTCThreadForCaller(ctx, r.PathValue("threadId"))
		if err != nil {
			writeExternalOTCStatusError(w, err)
			return
		}
		outIterations := make([]externalOTCIterationDTO, 0, len(iterations))
		for _, row := range iterations {
			outIterations = append(outIterations, externalOTCIterationToDTO(row))
		}
		var outContract any
		if contract != nil {
			outContract = externalOTCContractToDTO(*contract)
		}
		writeExternalOTCJSON(w, http.StatusOK, map[string]any{
			"thread":      externalOTCThreadToDTO(*thread),
			"iterations":  outIterations,
			"contract":    outContract,
		})
	})

	mux.HandleFunc("GET /internal/api/v1/otc/external/contracts", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := externalOTCInternalContext(r, apiKey)
		if !ok {
			writeExternalOTCJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		rows, err := srv.ListExternalOTCContractsForCaller(ctx, r.URL.Query().Get("status"))
		if err != nil {
			writeExternalOTCStatusError(w, err)
			return
		}
		out := make([]externalOTCContractDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, externalOTCContractToDTO(row))
		}
		writeExternalOTCJSON(w, http.StatusOK, map[string]any{"contracts": out})
	})

	mux.HandleFunc("POST /internal/api/v1/otc/external/outbound/offers", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := externalOTCInternalContext(r, apiKey)
		if !ok {
			writeExternalOTCJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var in createExternalOTCOfferHTTPRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeExternalOTCJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		thread, err := srv.CreateExternalOTCOfferForCaller(ctx, CreateExternalOTCOfferInput{
			RemoteBankCode:    in.RemoteBankCode,
			RemoteThreadID:    in.RemoteThreadID,
			RemoteUserRef:     in.RemoteUserRef,
			RemoteDisplayName: in.RemoteDisplayName,
			RemoteAccountRef:  in.RemoteAccountRef,
			BuyerAccountID:    in.BuyerAccountID,
			SellerHoldingID:   in.SellerHoldingID,
			SecurityTicker:    in.SecurityTicker,
			SecurityType:      in.SecurityType,
			Currency:          in.Currency,
			Quantity:          in.Quantity,
			PricePerUnit:      in.PricePerUnit,
			Premium:           in.Premium,
			SettlementDate:    in.SettlementDate,
		})
		if err != nil {
			writeExternalOTCStatusError(w, err)
			return
		}
		writeExternalOTCJSON(w, http.StatusCreated, externalOTCThreadToDTO(*thread))
	})

	mux.HandleFunc("POST /internal/api/v1/otc/external/outbound/actions/{threadId}/counter", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := externalOTCInternalContext(r, apiKey)
		if !ok {
			writeExternalOTCJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var in counterExternalOTCOfferHTTPRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeExternalOTCJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		thread, err := srv.CounterExternalOTCThreadForCaller(ctx, CounterExternalOTCThreadInput{
			ThreadID:       r.PathValue("threadId"),
			Quantity:       in.Quantity,
			PricePerUnit:   in.PricePerUnit,
			Premium:        in.Premium,
			SettlementDate: in.SettlementDate,
		})
		if err != nil {
			writeExternalOTCStatusError(w, err)
			return
		}
		writeExternalOTCJSON(w, http.StatusOK, externalOTCThreadToDTO(*thread))
	})

	mux.HandleFunc("POST /internal/api/v1/otc/external/outbound/actions/{threadId}/withdraw", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := externalOTCInternalContext(r, apiKey)
		if !ok {
			writeExternalOTCJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		thread, err := srv.WithdrawExternalOTCThreadForCaller(ctx, r.PathValue("threadId"))
		if err != nil {
			writeExternalOTCStatusError(w, err)
			return
		}
		writeExternalOTCJSON(w, http.StatusOK, externalOTCThreadToDTO(*thread))
	})

	mux.HandleFunc("POST /internal/api/v1/otc/external/outbound/actions/{threadId}/accept", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := externalOTCInternalContext(r, apiKey)
		if !ok {
			writeExternalOTCJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		thread, contract, err := srv.AcceptExternalOTCThreadForCaller(ctx, r.PathValue("threadId"))
		if err != nil {
			writeExternalOTCStatusError(w, err)
			return
		}
		writeExternalOTCJSON(w, http.StatusOK, map[string]any{
			"thread":   externalOTCThreadToDTO(*thread),
			"contract": externalOTCContractToDTO(*contract),
		})
	})

	mux.HandleFunc("POST /internal/api/v1/otc/external/contracts/{contractId}/exercise", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := externalOTCInternalContext(r, apiKey)
		if !ok {
			writeExternalOTCJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var in exerciseExternalOTCContractHTTPRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeExternalOTCJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		contract, err := srv.MarkExternalOTCContractExercisedForCaller(ctx, r.PathValue("contractId"), in.ExerciseOpID, time.Now().UTC())
		if err != nil {
			writeExternalOTCStatusError(w, err)
			return
		}
		writeExternalOTCJSON(w, http.StatusOK, externalOTCContractToDTO(*contract))
	})
}

func externalOTCInternalContext(r *http.Request, apiKey string) (context.Context, bool) {
	if apiKey != "" && strings.TrimSpace(r.Header.Get("X-Api-Key")) != strings.TrimSpace(apiKey) {
		return nil, false
	}
	email := strings.TrimSpace(r.Header.Get("X-User-Email"))
	if email == "" {
		return nil, false
	}
	return metadata.NewIncomingContext(r.Context(), metadata.Pairs("user-email", email)), true
}

func writeExternalOTCJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeExternalOTCStatusError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok {
		writeExternalOTCJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeExternalOTCJSON(w, externalOTCHTTPStatus(st.Code()), map[string]string{"error": st.Message()})
}

func externalOTCHTTPStatus(code codes.Code) int {
	switch code {
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.NotFound:
		return http.StatusNotFound
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.FailedPrecondition:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func externalOTCThreadToDTO(rec ExternalOTCThreadRecord) externalOTCThreadDTO {
	return externalOTCThreadDTO{
		ID:                 rec.ID,
		Direction:          rec.Direction,
		RemoteBankCode:     rec.RemoteBankCode,
		RemoteThreadID:     rec.RemoteThreadID,
		RemoteUserRef:      rec.RemoteUserRef,
		RemoteDisplayName:  rec.RemoteDisplayName,
		RemoteAccountRef:   rec.RemoteAccountRef,
		LocalUserID:        rec.LocalUserID,
		LocalUserKind:      rec.LocalUserKind,
		LocalAccountID:     rec.LocalAccountID,
		LocalAccountNumber: rec.LocalAccountNumber,
		LocalRole:          rec.LocalRole,
		SecurityID:         rec.SecurityID,
		SecurityTicker:     rec.SecurityTicker,
		SellerHoldingID:    rec.SellerHoldingID,
		Quantity:           rec.Quantity,
		PricePerUnit:       rec.PricePerUnit,
		Premium:            rec.Premium,
		Currency:           rec.Currency,
		SettlementDate:     rec.SettlementDate.Format("2006-01-02"),
		ModifiedBySide:     rec.ModifiedBySide,
		Status:             rec.Status,
		CreatedAt:          rec.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          rec.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func externalOTCIterationToDTO(rec ExternalOTCIterationRecord) externalOTCIterationDTO {
	return externalOTCIterationDTO{
		ID:             rec.ID,
		ThreadID:       rec.ThreadID,
		ProposedBySide: rec.ProposedBySide,
		Quantity:       rec.Quantity,
		PricePerUnit:   rec.PricePerUnit,
		Premium:        rec.Premium,
		SettlementDate: rec.SettlementDate.Format("2006-01-02"),
		CreatedAt:      rec.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func externalOTCContractToDTO(rec ExternalOTCContractRecord) externalOTCContractDTO {
	dto := externalOTCContractDTO{
		ID:                 rec.ID,
		ThreadID:           rec.ThreadID,
		Direction:          rec.Direction,
		RemoteBankCode:     rec.RemoteBankCode,
		RemoteThreadID:     rec.RemoteThreadID,
		RemoteUserRef:      rec.RemoteUserRef,
		RemoteDisplayName:  rec.RemoteDisplayName,
		RemoteAccountRef:   rec.RemoteAccountRef,
		LocalUserID:        rec.LocalUserID,
		LocalUserKind:      rec.LocalUserKind,
		LocalAccountID:     rec.LocalAccountID,
		LocalAccountNumber: rec.LocalAccountNumber,
		LocalRole:          rec.LocalRole,
		SecurityID:         rec.SecurityID,
		SecurityTicker:     rec.SecurityTicker,
		SellerHoldingID:    rec.SellerHoldingID,
		Quantity:           rec.Quantity,
		StrikePrice:        rec.StrikePrice,
		PremiumPaid:        rec.PremiumPaid,
		Currency:           rec.Currency,
		SettlementDate:     rec.SettlementDate.Format("2006-01-02"),
		AcceptedBySide:     rec.AcceptedBySide,
		Status:             rec.Status,
		PremiumOpID:        rec.PremiumOpID,
		ExerciseOpID:       rec.ExerciseOpID,
		CreatedAt:          rec.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          rec.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if rec.ExercisedAt != nil {
		dto.ExercisedAt = rec.ExercisedAt.UTC().Format(time.RFC3339)
	}
	return dto
}
