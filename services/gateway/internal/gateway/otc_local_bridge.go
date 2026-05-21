package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type externalOTCThreadDetail struct {
	Thread struct {
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
	} `json:"thread"`
	Iterations []struct {
		ID             string `json:"id"`
		ProposedBySide string `json:"proposedBySide"`
		Quantity       int64  `json:"quantity"`
		PricePerUnit   string `json:"pricePerUnit"`
		Premium        string `json:"premium"`
		SettlementDate string `json:"settlementDate"`
	} `json:"iterations"`
	Contract any `json:"contract"`
}

type externalOTCContract struct {
	ID                 string `json:"id"`
	ThreadID           string `json:"threadId"`
	RemoteBankCode     string `json:"remoteBankCode"`
	RemoteThreadID     string `json:"remoteThreadId"`
	RemoteUserRef      string `json:"remoteUserRef"`
	RemoteDisplayName  string `json:"remoteDisplayName"`
	LocalUserID        string `json:"localUserId"`
	LocalAccountID     string `json:"localAccountId"`
	LocalAccountNumber string `json:"localAccountNumber"`
	LocalRole          string `json:"localRole"`
	SecurityTicker     string `json:"securityTicker"`
	Quantity           int64  `json:"quantity"`
	StrikePrice        string `json:"strikePrice"`
	Currency           string `json:"currency"`
	SettlementDate     string `json:"settlementDate"`
	Status             string `json:"status"`
	ExerciseOpID       string `json:"exerciseOpId"`
}

func (s *Server) localBankOTCRequest(ctx context.Context, userEmail, method, path string, body []byte) (int, []byte, string, error) {
	baseURL := strings.TrimRight(s.BankInternalHTTPURL, "/")
	if baseURL == "" {
		return 0, nil, "", fmt.Errorf("bank internal HTTP URL not configured")
	}
	target := baseURL + path
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, "", err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-User-Email", strings.TrimSpace(userEmail))
	if s.BankInternalAPIKey != "" {
		req.Header.Set("X-Api-Key", s.BankInternalAPIKey)
	}
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, resp.Header.Get("Content-Type"), err
	}
	return resp.StatusCode, respBody, resp.Header.Get("Content-Type"), nil
}

func (s *Server) getLocalExternalOTCThread(ctx context.Context, userEmail, threadID string) (*externalOTCThreadDetail, error) {
	status, body, _, err := s.localBankOTCRequest(ctx, userEmail, http.MethodGet, "/internal/api/v1/otc/external/threads/"+url.PathEscape(threadID), nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("resolve external thread: HTTP %d %s", status, strings.TrimSpace(string(body)))
	}
	var out externalOTCThreadDetail
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Server) getLocalExternalOTCContract(ctx context.Context, userEmail, contractID string) (*externalOTCContract, error) {
	status, body, _, err := s.localBankOTCRequest(ctx, userEmail, http.MethodGet, "/internal/api/v1/otc/external/contracts", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("resolve external contract: HTTP %d %s", status, strings.TrimSpace(string(body)))
	}
	var out struct {
		Contracts []externalOTCContract `json:"contracts"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	for i := range out.Contracts {
		if out.Contracts[i].ID == contractID {
			return &out.Contracts[i], nil
		}
	}
	return nil, fmt.Errorf("resolve external contract: not found")
}
