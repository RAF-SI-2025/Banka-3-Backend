package gateway

import (
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultRoutingNumber = "333"
	defaultInterbankAuth = "dev-outbound-banka3"
)

func parseInterbankRoutes(raw string) map[string]string {
	routes := make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		routes[key] = strings.TrimRight(value, "/")
	}
	return routes
}

func ownRoutingNumber() string {
	if value := strings.TrimSpace(os.Getenv("BANK_ROUTING_NUMBER")); value != "" {
		return value
	}
	return defaultRoutingNumber
}

func interbankOutboundAPIKey() string {
	if value := strings.TrimSpace(os.Getenv("INTERBANK_API_KEY")); value != "" {
		return value
	}
	return defaultInterbankAuth
}

func newInterbankHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}
