package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type externalOTCExerciseRequest struct {
	ExerciseOpID string `json:"exerciseOpId"`
}

type banka2TransactionVote struct {
	Vote    string `json:"vote"`
	Reasons []struct {
		Reason string `json:"reason"`
	} `json:"reasons"`
}

func (s *Server) ExerciseExternalOtcContract(c *gin.Context) {
	var in externalOTCExerciseRequest
	if err := c.ShouldBindJSON(&in); err != nil && !strings.Contains(strings.ToLower(err.Error()), "eof") {
		writeBindError(c, err)
		return
	}

	bankCode := strings.TrimSpace(c.Param("bankCode"))
	contractID := strings.TrimSpace(c.Param("contractId"))
	if bankCode == "" || contractID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "bankCode and contractId are required"})
		return
	}
	if s.detectPartnerOTCProtocol(c.Request.Context(), bankCode) != partnerOTCProtocolBanka2 {
		notImplementedCelina5(c, "external OTC native partner flow still needs mapping in newestbackend")
		return
	}

	exerciseOpID := strings.TrimSpace(in.ExerciseOpID)
	if exerciseOpID == "" {
		exerciseOpID = "b2-exercise-" + contractID
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()

	remote, localMirror, mirrorErr, err := s.forwardBanka2ExternalOTCExercise(ctx, c, bankCode, contractID, exerciseOpID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"remote":      remote,
		"localMirror": localMirror,
		"mirrorError": mirrorErr,
	})
}

func (s *Server) forwardBanka2ExternalOTCExercise(ctx context.Context, c *gin.Context, bankCode, localContractID, exerciseOpID string) (map[string]any, any, string, error) {
	contract, err := s.getLocalExternalOTCContract(ctx, c.GetString("email"), localContractID)
	if err != nil {
		return nil, nil, "", err
	}
	if contract.RemoteThreadID == "" {
		return nil, nil, "", fmt.Errorf("local external contract %s is missing remoteThreadId", localContractID)
	}
	if contract.RemoteUserRef == "" {
		return nil, nil, "", fmt.Errorf("local external contract %s is missing remoteUserRef", localContractID)
	}
	if contract.SecurityTicker == "" || contract.StrikePrice == "" || contract.Currency == "" || contract.SettlementDate == "" {
		return nil, nil, "", fmt.Errorf("local external contract %s is missing required exercise data", localContractID)
	}
	if contract.LocalAccountNumber == "" {
		return nil, nil, "", fmt.Errorf("local external contract %s is missing localAccountNumber", localContractID)
	}

	myRouting, err := strconv.Atoi(strings.TrimSpace(s.RoutingNumber))
	if err != nil {
		return nil, nil, "", fmt.Errorf("invalid local bank code")
	}
	sellerRouting, err := strconv.Atoi(strings.TrimSpace(bankCode))
	if err != nil {
		return nil, nil, "", fmt.Errorf("invalid partner bank code")
	}

	remoteUserID := opaqueUserNumericID(contract.RemoteUserRef)
	if remoteUserID == "" {
		return nil, nil, "", fmt.Errorf("could not derive Banka 2 user id from %q", contract.RemoteUserRef)
	}
	total, err := multiplyDecimalString(contract.StrikePrice, contract.Quantity)
	if err != nil {
		return nil, nil, "", fmt.Errorf("invalid strike price: %w", err)
	}

	quantityNum := json.Number(strconv.FormatInt(contract.Quantity, 10))
	strikeNum := json.Number(contract.StrikePrice)
	totalNum := json.Number(total)
	txID := "b2-exercise:" + contract.RemoteThreadID

	tx := map[string]any{
		"postings": []any{
			map[string]any{
				"account": map[string]any{"type": "ACCOUNT", "num": contract.LocalAccountNumber},
				"amount":  totalNum,
				"asset":   map[string]any{"type": "MONAS", "asset": map[string]any{"currency": contract.Currency}},
			},
			map[string]any{
				"account": map[string]any{"type": "ACCOUNT", "num": contract.LocalAccountNumber},
				"amount":  json.Number("-" + total),
				"asset":   map[string]any{"type": "MONAS", "asset": map[string]any{"currency": contract.Currency}},
			},
			map[string]any{
				"account": map[string]any{
					"type": "PERSON",
					"id":   map[string]any{"routingNumber": sellerRouting, "id": remoteUserID},
				},
				"amount": json.Number("-" + quantityNum.String()),
				"asset":  map[string]any{"type": "STOCK", "asset": map[string]any{"ticker": contract.SecurityTicker}},
			},
			map[string]any{
				"account": map[string]any{
					"type": "PERSON",
					"id":   map[string]any{"routingNumber": myRouting, "id": contract.LocalUserID},
				},
				"amount": quantityNum,
				"asset":  map[string]any{"type": "STOCK", "asset": map[string]any{"ticker": contract.SecurityTicker}},
			},
			map[string]any{
				"account": map[string]any{
					"type": "OPTION",
					"id":   map[string]any{"routingNumber": sellerRouting, "id": contract.RemoteThreadID},
				},
				"amount": json.Number("-" + quantityNum.String()),
				"asset": map[string]any{
					"type": "OPTION",
					"asset": map[string]any{
						"negotiationId": map[string]any{"routingNumber": sellerRouting, "id": contract.RemoteThreadID},
						"stock":         map[string]any{"ticker": contract.SecurityTicker},
						"pricePerUnit":  map[string]any{"currency": contract.Currency, "amount": strikeNum},
						"settlementDate": contract.SettlementDate + "T00:00:00Z",
						"amount":         quantityNum,
					},
				},
			},
			map[string]any{
				"account": map[string]any{
					"type": "PERSON",
					"id":   map[string]any{"routingNumber": myRouting, "id": contract.LocalUserID},
				},
				"amount": quantityNum,
				"asset": map[string]any{
					"type": "OPTION",
					"asset": map[string]any{
						"negotiationId": map[string]any{"routingNumber": sellerRouting, "id": contract.RemoteThreadID},
						"stock":         map[string]any{"ticker": contract.SecurityTicker},
						"pricePerUnit":  map[string]any{"currency": contract.Currency, "amount": strikeNum},
						"settlementDate": contract.SettlementDate + "T00:00:00Z",
						"amount":         quantityNum,
					},
				},
			},
		},
		"transactionId": map[string]any{"routingNumber": myRouting, "id": txID},
		"message":       "OTC external exercise " + contract.RemoteThreadID,
		"paymentCode":   "OTC",
		"paymentPurpose": "External OTC exercise",
	}

	phase1Vote, phase1Status, err := s.sendBanka2InterbankMessage(ctx, bankCode, map[string]any{
		"idempotenceKey": map[string]any{
			"routingNumber":       myRouting,
			"locallyGeneratedKey": randomGatewayKey(),
		},
		"messageType": "NEW_TX",
		"message":     tx,
	})
	if err != nil {
		return nil, nil, "", err
	}

	remote := map[string]any{
		"phase1Status": phase1Status,
		"transactionId": map[string]any{
			"id":            txID,
			"routingNumber": myRouting,
		},
	}

	switch {
	case strings.EqualFold(phase1Vote.Vote, "YES"):
		if _, _, err := s.sendBanka2InterbankMessage(ctx, bankCode, map[string]any{
			"idempotenceKey": map[string]any{
				"routingNumber":       myRouting,
				"locallyGeneratedKey": randomGatewayKey(),
			},
			"messageType": "COMMIT_TX",
			"message": map[string]any{
				"transactionId": map[string]any{
					"routingNumber": myRouting,
					"id":            txID,
				},
			},
		}); err != nil {
			return nil, nil, "", err
		}
		remote["phase2"] = "committed"
	case onlyReason(phase1Vote.Reasons, "OPTION_USED_OR_EXPIRED"):
		remote["phase2"] = "already_committed_remote"
		remote["vote"] = phase1Vote.Vote
		remote["reasons"] = phase1Vote.Reasons
	default:
		remote["vote"] = phase1Vote.Vote
		remote["reasons"] = phase1Vote.Reasons
		return remote, nil, "remote bank rejected exercise", nil
	}

	payload, _ := json.Marshal(map[string]string{"exerciseOpId": exerciseOpID})
	localStatus, localBody, _, err := s.localBankOTCRequest(ctx, c.GetString("email"), http.MethodPost, "/internal/api/v1/otc/external/contracts/"+localContractID+"/exercise", payload)
	if err != nil {
		return remote, nil, err.Error(), nil
	}
	if localStatus < 200 || localStatus >= 300 {
		return remote, rawJSONOrNull(localBody), strings.TrimSpace(string(localBody)), nil
	}

	var localMirror any
	if len(bytes.TrimSpace(localBody)) > 0 {
		_ = json.Unmarshal(localBody, &localMirror)
	}
	return remote, localMirror, "", nil
}

func onlyReason(reasons []struct {
	Reason string `json:"reason"`
}, want string) bool {
	if len(reasons) == 0 {
		return false
	}
	for _, r := range reasons {
		if !strings.EqualFold(r.Reason, want) {
			return false
		}
	}
	return true
}

func opaqueUserNumericID(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "C-") || strings.HasPrefix(raw, "E-") {
		return strings.TrimSpace(raw[2:])
	}
	return raw
}

func multiplyDecimalString(amount string, quantity int64) (string, error) {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok {
		return "", fmt.Errorf("invalid decimal %q", amount)
	}
	rat.Mul(rat, new(big.Rat).SetInt64(quantity))
	return rat.FloatString(4), nil
}

func (s *Server) sendBanka2InterbankMessage(ctx context.Context, bankCode string, payload map[string]any) (banka2TransactionVote, int, error) {
	var vote banka2TransactionVote
	baseURL := strings.TrimRight(s.InterbankRoutes[bankCode], "/")
	if baseURL == "" {
		return vote, 0, fmt.Errorf("unknown partner bank")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return vote, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/interbank", bytes.NewReader(body))
	if err != nil {
		return vote, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.InterbankAPIKey != "" {
		req.Header.Set("X-Api-Key", s.InterbankAPIKey)
	}
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return vote, 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return vote, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return vote, resp.StatusCode, fmt.Errorf("partner /interbank HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if len(bytes.TrimSpace(respBody)) == 0 {
		return vote, resp.StatusCode, nil
	}
	if err := json.Unmarshal(respBody, &vote); err != nil {
		return vote, resp.StatusCode, err
	}
	return vote, resp.StatusCode, nil
}

func randomGatewayKey() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(buf)
}
