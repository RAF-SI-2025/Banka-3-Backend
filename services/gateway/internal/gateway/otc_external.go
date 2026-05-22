package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type externalDiscoveryBank struct {
	BankCode string                   `json:"bankCode"`
	Holdings []externalDiscoveryOffer `json:"holdings"`
}

type externalDiscoveryOffer struct {
	SellerBankPrefix  string `json:"sellerBankPrefix"`
	SellerID          string `json:"sellerId"`
	SellerDisplayName string `json:"sellerDisplayName"`
	SecurityTicker    string `json:"securityTicker"`
	AvailableCount    int64  `json:"availableCount"`
	Currency          string `json:"currency,omitempty"`
	CurrentPrice      any    `json:"currentPrice,omitempty"`
}

type createExternalOtcOfferRequest struct {
	BankCode          string `json:"bankCode"`
	SellerHoldingID   string `json:"sellerHoldingId"`
	SellerUserRef     string `json:"sellerUserRef"`
	SellerDisplayName string `json:"sellerDisplayName"`
	BuyerAccountID    string `json:"buyerAccountId"`
	SecurityTicker    string `json:"securityTicker"`
	SecurityType      string `json:"securityType"`
	Currency          string `json:"currency"`
	Quantity          int64  `json:"quantity"`
	PricePerUnit      string `json:"pricePerUnit"`
	Premium           string `json:"premium"`
	SettlementDate    string `json:"settlementDate"`
}

type counterExternalOtcOfferRequest struct {
	Quantity       int64  `json:"quantity"`
	PricePerUnit   string `json:"pricePerUnit"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlementDate"`
}

func (s *Server) ListExternalPublicHoldings(c *gin.Context) {
	if len(s.InterbankRoutes) == 0 {
		c.JSON(http.StatusOK, gin.H{"banks": []externalDiscoveryBank{}})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()

	banks := make([]externalDiscoveryBank, 0, len(s.InterbankRoutes))
	for bankCode, baseURL := range s.InterbankRoutes {
		holdings, err := s.fetchPartnerPublicStocks(ctx, bankCode, baseURL)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"message": fmt.Sprintf("external discovery failed for bank %s: %v", bankCode, err),
			})
			return
		}
		banks = append(banks, externalDiscoveryBank{
			BankCode: bankCode,
			Holdings: holdings,
		})
	}

	c.JSON(http.StatusOK, gin.H{"banks": banks})
}

func (s *Server) CreateExternalOtcOffer(c *gin.Context) {
	var in createExternalOtcOfferRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		writeBindError(c, err)
		return
	}
	if strings.TrimSpace(in.BankCode) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "bankCode is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	if s.detectPartnerOTCProtocol(ctx, in.BankCode) != partnerOTCProtocolBanka2 {
		notImplementedCelina5(c, "external OTC native partner flow still needs mapping in newestbackend")
		return
	}

	_, remoteBody, _, err := s.createBanka2ExternalOTCOffer(ctx, c, in)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	remoteThreadID, err := extractBanka2RemoteThreadID(remoteBody)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}

	mirrorPayload := map[string]any{
		"remoteBankCode":    in.BankCode,
		"remoteThreadId":    remoteThreadID,
		"remoteUserRef":     in.SellerUserRef,
		"remoteDisplayName": in.SellerDisplayName,
		"buyerAccountId":    in.BuyerAccountID,
		"sellerHoldingId":   in.SellerHoldingID,
		"securityTicker":    in.SecurityTicker,
		"securityType":      firstNonEmptyString(in.SecurityType, "stock"),
		"currency":          firstNonEmptyString(in.Currency, "USD"),
		"quantity":          in.Quantity,
		"pricePerUnit":      in.PricePerUnit,
		"premium":           in.Premium,
		"settlementDate":    in.SettlementDate,
	}
	payload, _ := json.Marshal(mirrorPayload)
	status, localBody, _, err := s.localBankOTCRequest(ctx, c.GetString("email"), http.MethodPost, "/internal/api/v1/otc/external/outbound/offers", payload)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	if status < 200 || status >= 300 {
		c.Data(status, "application/json; charset=utf-8", localBody)
		return
	}

	var localMirror any
	_ = json.Unmarshal(localBody, &localMirror)
	var remote any
	_ = json.Unmarshal(remoteBody, &remote)
	c.JSON(http.StatusCreated, gin.H{
		"remote":      remote,
		"localMirror": localMirror,
		"mirrorError": "",
	})
}

func (s *Server) ListExternalOtcThreads(c *gin.Context) {
	path := "/internal/api/v1/otc/external/threads"
	if statusParam := strings.TrimSpace(c.Query("status")); statusParam != "" {
		path += "?status=" + statusParam
	}
	status, body, contentType, err := s.localBankOTCRequest(c.Request.Context(), c.GetString("email"), http.MethodGet, path, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	c.Data(status, firstNonEmptyString(contentType, "application/json; charset=utf-8"), body)
}

func (s *Server) GetExternalOtcThread(c *gin.Context) {
	threadID := strings.TrimSpace(c.Param("threadId"))
	if threadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "threadId is required"})
		return
	}
	status, body, contentType, err := s.localBankOTCRequest(c.Request.Context(), c.GetString("email"), http.MethodGet, "/internal/api/v1/otc/external/threads/"+threadID, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	c.Data(status, firstNonEmptyString(contentType, "application/json; charset=utf-8"), body)
}

func (s *Server) CounterExternalOtcOffer(c *gin.Context) {
	var in counterExternalOtcOfferRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		writeBindError(c, err)
		return
	}
	bankCode := strings.TrimSpace(c.Param("bankCode"))
	threadID := strings.TrimSpace(c.Param("threadId"))
	if bankCode == "" || threadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "bankCode and threadId are required"})
		return
	}
	if s.detectPartnerOTCProtocol(c.Request.Context(), bankCode) != partnerOTCProtocolBanka2 {
		notImplementedCelina5(c, "external OTC native partner flow still needs mapping in newestbackend")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	threadDetail, err := s.getLocalExternalOTCThread(ctx, c.GetString("email"), threadID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	status, remoteBody, _, err := s.forwardBanka2ExternalOTCAction(ctx, c, bankCode, threadDetail, "counter", in)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	if status < 200 || status >= 300 {
		c.Data(status, "application/json; charset=utf-8", remoteBody)
		return
	}

	payload, _ := json.Marshal(in)
	localStatus, localBody, _, err := s.localBankOTCRequest(ctx, c.GetString("email"), http.MethodPost, "/internal/api/v1/otc/external/outbound/actions/"+threadID+"/counter", payload)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	if localStatus < 200 || localStatus >= 300 {
		c.Data(localStatus, "application/json; charset=utf-8", localBody)
		return
	}
	var localMirror any
	_ = json.Unmarshal(localBody, &localMirror)
	c.JSON(http.StatusOK, gin.H{"remote": rawJSONOrNull(remoteBody), "localMirror": localMirror, "mirrorError": ""})
}

func (s *Server) WithdrawExternalOtcOffer(c *gin.Context) {
	s.forwardSimpleExternalOTCAction(c, "withdraw")
}

func (s *Server) AcceptExternalOtcOffer(c *gin.Context) {
	s.forwardSimpleExternalOTCAction(c, "accept")
}

func (s *Server) ListExternalOtcContracts(c *gin.Context) {
	path := "/internal/api/v1/otc/external/contracts"
	if statusParam := strings.TrimSpace(c.Query("status")); statusParam != "" {
		path += "?status=" + statusParam
	}
	status, body, contentType, err := s.localBankOTCRequest(c.Request.Context(), c.GetString("email"), http.MethodGet, path, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	c.Data(status, firstNonEmptyString(contentType, "application/json; charset=utf-8"), body)
}

func notImplementedCelina5(c *gin.Context, message string) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": message})
}

func (s *Server) fetchPartnerPublicStocks(ctx context.Context, bankCode, baseURL string) ([]externalDiscoveryOffer, error) {
	switch s.detectPartnerOTCProtocol(ctx, bankCode) {
	case partnerOTCProtocolUnknown:
		return nil, fmt.Errorf("partner OTC protocol not detected")
	case partnerOTCProtocolNative:
		return []externalDiscoveryOffer{}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/public-stock", nil)
	if err != nil {
		return nil, err
	}
	if s.InterbankAPIKey != "" {
		req.Header.Set("X-Api-Key", s.InterbankAPIKey)
	}
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("partner returned status %d", resp.StatusCode)
	}

	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	rows, ok := payload.([]any)
	if !ok {
		return []externalDiscoveryOffer{}, nil
	}

	out := make([]externalDiscoveryOffer, 0)
	for _, rawItem := range rows {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		stock, _ := item["stock"].(map[string]any)
		ticker := firstString(stock, "ticker", "symbol", "securityTicker", "name")
		currency := firstString(stock, "currency", "currencyCode")
		currentPrice := firstValue(stock, "currentPrice", "pricePerUnit", "price")
		sellers, _ := item["sellers"].([]any)
		for _, rawSeller := range sellers {
			sellerRow, ok := rawSeller.(map[string]any)
			if !ok {
				continue
			}
			sellerMap, _ := sellerRow["seller"].(map[string]any)
			sellerID := firstString(sellerMap, "id")
			if sellerID == "" {
				sellerID = firstString(sellerRow, "sellerId", "remoteUserRef")
			}
			displayName := firstString(sellerRow, "sellerDisplayName", "displayName", "name")
			if displayName == "" {
				if routing := firstInt64(sellerMap, "routingNumber"); routing != 0 && sellerID != "" {
					if resolved, err := s.fetchBanka2UserDisplayName(ctx, baseURL, int(routing), sellerID); err == nil && strings.TrimSpace(resolved) != "" {
						displayName = resolved
					}
				}
			}
			if displayName == "" {
				displayName = sellerID
			}
			out = append(out, externalDiscoveryOffer{
				SellerBankPrefix:  bankCode,
				SellerID:          sellerID,
				SellerDisplayName: displayName,
				SecurityTicker:    ticker,
				AvailableCount:    firstInt64(sellerRow, "amount", "availableCount", "quantity"),
				Currency:          currency,
				CurrentPrice:      currentPrice,
			})
		}
	}
	return out, nil
}

func (s *Server) forwardSimpleExternalOTCAction(c *gin.Context, action string) {
	bankCode := strings.TrimSpace(c.Param("bankCode"))
	threadID := strings.TrimSpace(c.Param("threadId"))
	if bankCode == "" || threadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "bankCode and threadId are required"})
		return
	}
	if s.detectPartnerOTCProtocol(c.Request.Context(), bankCode) != partnerOTCProtocolBanka2 {
		notImplementedCelina5(c, "external OTC native partner flow still needs mapping in newestbackend")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	threadDetail, err := s.getLocalExternalOTCThread(ctx, c.GetString("email"), threadID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	status, remoteBody, _, err := s.forwardBanka2ExternalOTCAction(ctx, c, bankCode, threadDetail, action, counterExternalOtcOfferRequest{})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	if status < 200 || status >= 300 {
		c.Data(status, "application/json; charset=utf-8", remoteBody)
		return
	}

	localStatus, localBody, _, err := s.localBankOTCRequest(ctx, c.GetString("email"), http.MethodPost, "/internal/api/v1/otc/external/outbound/actions/"+threadID+"/"+action, []byte(`{}`))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	if localStatus < 200 || localStatus >= 300 {
		c.Data(localStatus, "application/json; charset=utf-8", localBody)
		return
	}
	var localMirror any
	_ = json.Unmarshal(localBody, &localMirror)
	c.JSON(http.StatusOK, gin.H{"remote": rawJSONOrNull(remoteBody), "localMirror": localMirror, "mirrorError": ""})
}

func (s *Server) createBanka2ExternalOTCOffer(ctx context.Context, c *gin.Context, in createExternalOtcOfferRequest) (int, []byte, string, error) {
	myRouting, err := strconv.Atoi(strings.TrimSpace(s.RoutingNumber))
	if err != nil {
		return 0, nil, "", fmt.Errorf("invalid local bank code")
	}
	sellerRouting, err := strconv.Atoi(strings.TrimSpace(in.BankCode))
	if err != nil {
		return 0, nil, "", fmt.Errorf("invalid partner bank code")
	}
	currency := firstNonEmptyString(in.Currency, "USD")
	payload := map[string]any{
		"stock": map[string]any{
			"ticker": in.SecurityTicker,
		},
		"settlementDate": in.SettlementDate + "T00:00:00Z",
		"pricePerUnit": map[string]any{
			"currency": currency,
			"amount":   json.Number(strings.TrimSpace(in.PricePerUnit)),
		},
		"premium": map[string]any{
			"currency": currency,
			"amount":   json.Number(strings.TrimSpace(in.Premium)),
		},
		"buyerId": map[string]any{
			"routingNumber": myRouting,
			"id":            localOpaqueCallerID(c),
		},
		"sellerId": map[string]any{
			"routingNumber": sellerRouting,
			"id":            strings.TrimSpace(in.SellerUserRef),
		},
		"amount": json.Number(strconv.FormatInt(in.Quantity, 10)),
		"lastModifiedBy": map[string]any{
			"routingNumber": myRouting,
			"id":            localOpaqueCallerID(c),
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, "", err
	}
	return s.partnerOTCRequestWithMethod(ctx, in.BankCode, http.MethodPost, "/negotiations", body)
}

func (s *Server) forwardBanka2ExternalOTCAction(ctx context.Context, c *gin.Context, bankCode string, threadDetail *externalOTCThreadDetail, action string, counter counterExternalOtcOfferRequest) (int, []byte, string, error) {
	remoteThreadID := strings.TrimSpace(threadDetail.Thread.RemoteThreadID)
	if remoteThreadID == "" {
		return 0, nil, "", fmt.Errorf("local mirror is missing remoteThreadId")
	}
	path := "/negotiations/" + url.PathEscape(bankCode) + "/" + url.PathEscape(remoteThreadID)

	switch action {
	case "counter":
		myRouting, err := strconv.Atoi(strings.TrimSpace(s.RoutingNumber))
		if err != nil {
			return 0, nil, "", fmt.Errorf("invalid local bank code")
		}
		sellerRouting, err := strconv.Atoi(strings.TrimSpace(bankCode))
		if err != nil {
			return 0, nil, "", fmt.Errorf("invalid partner bank code")
		}
		payload := map[string]any{
			"stock": map[string]any{
				"ticker": threadDetail.Thread.SecurityTicker,
			},
			"settlementDate": counter.SettlementDate + "T00:00:00Z",
			"pricePerUnit": map[string]any{
				"currency": firstNonEmptyString(threadDetail.Thread.Currency, "USD"),
				"amount":   json.Number(strings.TrimSpace(counter.PricePerUnit)),
			},
			"premium": map[string]any{
				"currency": firstNonEmptyString(threadDetail.Thread.Currency, "USD"),
				"amount":   json.Number(strings.TrimSpace(counter.Premium)),
			},
			"buyerId": map[string]any{
				"routingNumber": myRouting,
				"id":            localOpaqueCallerID(c),
			},
			"sellerId": map[string]any{
				"routingNumber": sellerRouting,
				"id":            strings.TrimSpace(threadDetail.Thread.RemoteUserRef),
			},
			"amount": json.Number(strconv.FormatInt(counter.Quantity, 10)),
			"lastModifiedBy": map[string]any{
				"routingNumber": myRouting,
				"id":            localOpaqueCallerID(c),
			},
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, "", err
		}
		return s.partnerOTCRequestWithMethod(ctx, bankCode, http.MethodPut, path, body)
	case "withdraw":
		return s.partnerOTCRequestWithMethod(ctx, bankCode, http.MethodDelete, path, nil)
	case "accept":
		return s.partnerOTCRequestWithMethod(ctx, bankCode, http.MethodGet, path+"/accept", nil)
	default:
		return 0, nil, "", fmt.Errorf("unsupported external OTC action %q", action)
	}
}

func extractBanka2RemoteThreadID(body []byte) (string, error) {
	var raw struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.ID) == "" {
		return "", fmt.Errorf("partner response missing remote thread id")
	}
	return raw.ID, nil
}

func localOpaqueCallerID(c *gin.Context) string {
	email := strings.TrimSpace(c.GetString("email"))
	role := strings.ToLower(strings.TrimSpace(c.GetString("role")))
	if role == "employee" {
		return "E-" + email
	}
	return "C-" + email
}

func rawJSONOrNull(body []byte) any {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.RawMessage(body)
}

func firstValue(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func firstInt64(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch x := v.(type) {
			case float64:
				return int64(x)
			case int64:
				return x
			case int:
				return int64(x)
			case json.Number:
				i, _ := x.Int64()
				return i
			}
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
