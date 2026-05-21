package bank

import "strings"

// accountBankCodeForRouting extracts the three-digit bank prefix used for
// Celina 5 inter-bank routing. Unlike the rewrite version, the fresh backend
// does not currently expose a shared account-validation package, so this helper
// intentionally does the minimal safe parse needed for routing decisions.
func accountBankCodeForRouting(number string) (string, bool) {
	number = strings.TrimSpace(number)
	if len(number) < 3 {
		return "", false
	}
	for i := 0; i < 3; i++ {
		if number[i] < '0' || number[i] > '9' {
			return "", false
		}
	}
	return number[:3], true
}
