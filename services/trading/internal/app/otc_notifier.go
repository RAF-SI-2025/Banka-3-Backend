// OTC notifier adapter.
//
// Wires service.OTCNotifier to an email.Sender — either pkg/email
// directly or notification-svc, decided by app wiring. Email body is
// Serbian (the user-facing language for this product).
//
// Recipient resolution
// ====================
// Trading-svc does not own user identities; we dial user-svc to
// resolve display_name + email. Same admin-metadata sentinel as the
// other trading→user adapters in this package.

package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// otcEmailNotifier emits Serbian counterparty-facing emails on OTC
// events. Resolves recipient email via user-svc and dispatches through
// the email.Sender; failures are logged and swallowed so a flaky email
// path doesn't roll back a successful saga.
type otcEmailNotifier struct {
	sender email.Sender
	users  userpb.UserServiceClient
	log    *slog.Logger
}

// newOTCEmailNotifier wires the adapter. Returns nil-safe; callers
// should always assign to svc.OTCNotifier rather than guarding at the
// call site.
func newOTCEmailNotifier(sender email.Sender, users userpb.UserServiceClient, log *slog.Logger) service.OTCNotifier {
	if sender == nil {
		return nil
	}
	return &otcEmailNotifier{sender: sender, users: users, log: log}
}

func (n *otcEmailNotifier) OnOTCCounterOffer(ctx context.Context, o *domain.OTCOffer, recipientID string, kind domain.UserKind) {
	if n == nil {
		return
	}
	addr, err := n.recipientEmail(ctx, recipientID, kind)
	if err != nil || addr == "" {
		n.log.Warn("otc email: skip counter-offer (recipient unresolved)",
			"recipient_id", recipientID, "err", errString(err))
		return
	}
	body := fmt.Sprintf(
		"Stigla je nova OTC ponuda u niti %s.\n\nKoličina: %d\nCena po jedinici: %s %s\nPremija: %s %s\nDatum izvršenja: %s\n\nOtvorite portal da pregledate detalje.",
		o.ThreadID, o.Quantity, o.PricePerUnit, o.Currency, o.Premium, o.Currency, o.SettlementDate.Format("2006-01-02"),
	)
	n.dispatch(ctx, addr, "Nova OTC ponuda", body)
}

func (n *otcEmailNotifier) OnOTCAccepted(ctx context.Context, c *domain.OTCContract, recipientID string, kind domain.UserKind) {
	if n == nil {
		return
	}
	addr, err := n.recipientEmail(ctx, recipientID, kind)
	if err != nil || addr == "" {
		n.log.Warn("otc email: skip accepted (recipient unresolved)",
			"recipient_id", recipientID, "err", errString(err))
		return
	}
	body := fmt.Sprintf(
		"OTC ponuda je prihvaćena i ugovor je potpisan.\n\nUgovor: %s\nKoličina: %d\nStrike: %s %s\nPremija plaćena: %s %s\nDatum izvršenja: %s",
		c.ID, c.Quantity, c.StrikePrice, c.Currency, c.PremiumPaid, c.Currency, c.SettlementDate.Format("2006-01-02"),
	)
	n.dispatch(ctx, addr, "OTC ugovor sklopljen", body)
}

func (n *otcEmailNotifier) OnOTCWithdrawn(ctx context.Context, o *domain.OTCOffer, recipientID string, kind domain.UserKind) {
	if n == nil {
		return
	}
	addr, err := n.recipientEmail(ctx, recipientID, kind)
	if err != nil || addr == "" {
		n.log.Warn("otc email: skip withdrawn (recipient unresolved)",
			"recipient_id", recipientID, "err", errString(err))
		return
	}
	body := fmt.Sprintf("Druga strana je odustala od OTC pregovora u niti %s.", o.ThreadID)
	n.dispatch(ctx, addr, "OTC nit odustana", body)
}

func (n *otcEmailNotifier) OnOTCContractExpired(ctx context.Context, c *domain.OTCContract, recipientID string, kind domain.UserKind) {
	if n == nil {
		return
	}
	addr, err := n.recipientEmail(ctx, recipientID, kind)
	if err != nil || addr == "" {
		n.log.Warn("otc email: skip expired (recipient unresolved)",
			"recipient_id", recipientID, "err", errString(err))
		return
	}
	body := fmt.Sprintf(
		"Vaš OTC ugovor %s je istekao bez izvršenja na datum %s. Premija od %s %s ostaje nepovratna.",
		c.ID, c.SettlementDate.Format("2006-01-02"), c.PremiumPaid, c.Currency,
	)
	n.dispatch(ctx, addr, "OTC ugovor istekao", body)
}

func (n *otcEmailNotifier) recipientEmail(ctx context.Context, userID string, kind domain.UserKind) (string, error) {
	if n.users == nil {
		return "", nil
	}
	ctx = withUserAdmin(ctx)
	// Cap RPCs to a few seconds so a misbehaving user-svc can't pin the
	// saga's notification post-step.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch kind {
	case domain.KindClient:
		r, err := n.users.GetClient(ctx, &userpb.GetClientRequest{Id: userID})
		if err != nil {
			return "", err
		}
		return r.GetEmail(), nil
	case domain.KindEmployee:
		r, err := n.users.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: userID})
		if err != nil {
			return "", err
		}
		return r.GetEmail(), nil
	}
	return "", nil
}

func (n *otcEmailNotifier) dispatch(ctx context.Context, to, subject, body string) {
	if err := n.sender.Send(ctx, email.Message{To: to, Subject: subject, Body: body}); err != nil {
		n.log.Warn("otc email send failed", "to", to, "subject", subject, "err", err.Error())
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
