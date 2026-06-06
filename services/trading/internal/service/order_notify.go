package service

import (
	"context"
	"fmt"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// Order lifecycle notifications (todoSpec C3 S20-S25).
//
// Each helper is best-effort: it nil-checks s.Notifier and never returns
// an error, so a notification failure can't fail the order operation that
// triggered it. Delivery is the in-app row (always, when a Notifier is
// wired); email is only attempted when an address is readily available.
//
// The trading service has no email-address resolver, so for order events
// we generally have no `to` address — the in-app notification is what
// satisfies these scenarios. The email leg is left as a deliberate no-op
// (see notifyOrderEmail) until an address resolver is wired here.
//
// eventKind is always "order" so the FE can group these rows.
const orderEventKind = "order"

// notifyOrder writes the in-app row for an order event to the order's
// owner. Best-effort: logs on error, never propagates.
func (s *Service) notifyOrder(ctx context.Context, o *domain.Order, title, body string) {
	if s == nil || s.Notifier == nil || o == nil {
		return
	}
	if err := s.Notifier.InApp(ctx, o.UserID, o.UserKind, orderEventKind, title, body); err != nil {
		s.Log.Warn("order notify: in-app delivery failed",
			"order_id", o.ID, "user_id", o.UserID, "title", title, "err", err.Error())
	}
	// Email is intentionally skipped: the trading service has no
	// email-address resolver, so we have no `to` here. When one is wired,
	// resolve the owner's address and call s.Notifier.Email(...).
}

// notifyOrderPending — S20. An agent's order needing supervisor approval
// has just been created in status Pending.
func (s *Service) notifyOrderPending(ctx context.Context, o *domain.Order) {
	s.notifyOrder(ctx, o,
		"Nalog čeka odobrenje supervizora",
		"Vaš nalog je kreiran i čeka odobrenje supervizora pre izvršenja.")
}

// notifyOrderApproved — S21. A supervisor approved the order.
func (s *Service) notifyOrderApproved(ctx context.Context, o *domain.Order) {
	s.notifyOrder(ctx, o,
		"Vaš nalog je odobren",
		"Supervizor je odobrio vaš nalog. Izvršenje će uskoro početi.")
}

// notifyOrderDeclined — S22. A supervisor declined the order.
func (s *Service) notifyOrderDeclined(ctx context.Context, o *domain.Order) {
	s.notifyOrder(ctx, o,
		"Vaš nalog je odbijen",
		"Supervizor je odbio vaš nalog. Nalog neće biti izvršen.")
}

// notifyOrderDone — S23. The order is fully executed (remaining == 0).
func (s *Service) notifyOrderDone(ctx context.Context, o *domain.Order) {
	s.notifyOrder(ctx, o,
		"Nalog je u potpunosti izvršen",
		fmt.Sprintf("Vaš nalog je u potpunosti izvršen (ukupno %d jedinica).", o.Quantity))
}

// notifyOrderPartialFill — S24. A fill landed that didn't complete the
// order. The body MUST carry how many units executed in this fill and how
// many remain.
func (s *Service) notifyOrderPartialFill(ctx context.Context, o *domain.Order, executedQty, remainingQty int32) {
	s.notifyOrder(ctx, o,
		"Delimično izvršenje naloga",
		partialFillBody(executedQty, remainingQty))
}

// partialFillBody renders the S24 body. Split out so the count formatting
// is unit-testable without a wired Notifier.
func partialFillBody(executedQty, remainingQty int32) string {
	return fmt.Sprintf(
		"Delimično je izvršeno %d jedinica vašeg naloga. Preostalo za izvršenje: %d jedinica.",
		executedQty, remainingQty)
}

// notifyOrderAutoCancelled — S25. The system auto-cancelled the order
// (e.g. the security's settlement date passed, or a fill hit a permanent
// settlement error). The body MUST carry the reason.
func (s *Service) notifyOrderAutoCancelled(ctx context.Context, o *domain.Order, reason string) {
	body := "Vaš nalog je otkazan od strane sistema."
	if reason != "" {
		body = fmt.Sprintf("Vaš nalog je otkazan od strane sistema. Razlog: %s", reason)
	}
	s.notifyOrder(ctx, o, "Nalog je otkazan", body)
}
