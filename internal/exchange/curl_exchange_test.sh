#!/bin/bash

# Konfiguracija
BASE_URL="http://localhost:8081/api" 
GREEN='\033[0;32m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')] INFO: $1${NC}"
}

log_success() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')] SUCCESS: $1${NC}"
}

log_error() {
    echo -e "${RED}[$(date +'%Y-%m-%d %H:%M:%S')] ERROR: $1${NC}"
}

echo "==========================================="
echo "   EXCHANGE SERVICE GATEWAY TESTER"
echo "==========================================="

# 1. Test GET Kursna Lista
log_info "Testiranje GET /exchange/rates..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X GET "${BASE_URL}/exchange/rates")
HTTP_STATUS=$(echo "$RESPONSE" | tail -n 1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_STATUS" -eq 200 ]; then
    log_success "Kursna lista dobijena (Status: $HTTP_STATUS)"
    echo "$BODY" | jq . 2>/dev/null || echo "$BODY"
else
    log_error "Neuspešno dohvatanje kursne liste (Status: $HTTP_STATUS)"
    echo "$BODY"
fi

echo "-------------------------------------------"

# 2. Test POST Konverzija
log_info "Testiranje POST /exchange/convert (EUR -> RSD, 100 units)..."
CONVERT_DATA='{
    "from_currency": "EUR",
    "to_currency": "RSD",
    "amount": 100.0
}'

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${BASE_URL}/exchange/convert" \
    -H "Content-Type: application/json" \
    -d "$CONVERT_DATA")

HTTP_STATUS=$(echo "$RESPONSE" | tail -n 1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_STATUS" -eq 200 ]; then
    log_success "Konverzija uspešna (Status: $HTTP_STATUS)"
    echo "$BODY" | jq . 2>/dev/null || echo "$BODY"
else
    log_error "Greška pri konverziji (Status: $HTTP_STATUS)"
    echo "$BODY"
fi

echo "-------------------------------------------"

# 3. Test ERROR case (Negativan iznos)
log_info "Testiranje validacije (Negativan iznos)..."
INVALID_DATA='{"from_currency":"USD", "to_currency":"EUR", "amount":-10}'

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${BASE_URL}/exchange/convert" \
    -H "Content-Type: application/json" \
    -d "$INVALID_DATA")

HTTP_STATUS=$(echo "$RESPONSE" | tail -n 1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_STATUS" -ge 400 ]; then
    log_success "Validacija radi. Server odbio negativan iznos (Status: $HTTP_STATUS)"
    echo "$BODY"
else
    log_error "Server je prihvatio nevalidan iznos! (Status: $HTTP_STATUS)"
fi

echo "==========================================="
log_info "Testiranje završeno."
