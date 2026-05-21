package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type partnerOTCProtocol string

const (
	partnerOTCProtocolUnknown partnerOTCProtocol = ""
	partnerOTCProtocolNative  partnerOTCProtocol = "native"
	partnerOTCProtocolBanka2  partnerOTCProtocol = "banka2"
)

func (s *Server) detectPartnerOTCProtocol(ctx context.Context, bankCode string) partnerOTCProtocol {
	baseURL := strings.TrimRight(s.InterbankRoutes[bankCode], "/")
	if baseURL == "" {
		return partnerOTCProtocolUnknown
	}

	// Fresh Celina 5 / native integrations expose the old compatibility path.
	nativeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/bank/api/v1/otc/public", nil)
	if err == nil {
		if resp, err := s.HTTPClient.Do(nativeReq); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return partnerOTCProtocolNative
			}
		}
	}

	// Banka 2 style protocol.
	banka2Req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/public-stock", nil)
	if err == nil {
		if s.InterbankAPIKey != "" {
			banka2Req.Header.Set("X-Api-Key", s.InterbankAPIKey)
		}
		if resp, err := s.HTTPClient.Do(banka2Req); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return partnerOTCProtocolBanka2
			}
		}
	}

	return partnerOTCProtocolUnknown
}

func (s *Server) partnerOTCRequestWithMethod(ctx context.Context, bankCode, method, path string, body []byte) (int, []byte, string, error) {
	baseURL := strings.TrimRight(s.InterbankRoutes[bankCode], "/")
	if baseURL == "" {
		return 0, nil, "", fmt.Errorf("unknown partner bank")
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, "", err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.InterbankAPIKey != "" {
		req.Header.Set("X-Api-Key", s.InterbankAPIKey)
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

func (s *Server) fetchBanka2UserDisplayName(ctx context.Context, baseURL string, routingNumber int, id string) (string, error) {
	type userInfo struct {
		DisplayName string `json:"displayName"`
	}
	target := strings.TrimRight(baseURL, "/") + "/user/" + strconv.Itoa(routingNumber) + "/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	if s.InterbankAPIKey != "" {
		req.Header.Set("X-Api-Key", s.InterbankAPIKey)
	}
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out userInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.DisplayName, nil
}
