package bank

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/theplant/luhn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var CardSpecs = map[card_brand]struct {
	Prefixes []string
	Length   int
}{
	visa:       {Prefixes: []string{"4"}, Length: 16},
	mastercard: {Prefixes: []string{"51", "52", "53", "54", "55"}, Length: 16},
	dinacard:   {Prefixes: []string{"9891"}, Length: 16},
	amex:       {Prefixes: []string{"34", "37"}, Length: 15},
}

func GenerateCardNumber(cardBrand card_brand, accountNum string) (string, error) {
	spec, ok := CardSpecs[cardBrand]
	if !ok {
		return "", fmt.Errorf("invalid card type: %v", cardBrand)
	}

	prefix := spec.Prefixes[0]
	dataLength := spec.Length - 1

	partialStr := prefix + accountNum
	if len(partialStr) > dataLength {
		partialStr = partialStr[:dataLength]
	} else if len(partialStr) < dataLength {
		partialStr = partialStr + strings.Repeat("0", dataLength-len(partialStr))
	}

	val, err := strconv.ParseInt(partialStr, 10, 64)
	if err != nil {
		return "", fmt.Errorf("failed to parse partial card number: %v", err)
	}

	checkDigit := luhn.CalculateLuhn(int(val))

	return fmt.Sprintf("%s%d", partialStr, checkDigit), nil
}

func GenerateCVV() string {
	return fmt.Sprintf("%03d", rand.Intn(1000))
}

func mapCardToProto(card *Card) *bankpb.CardResponse {
	if card == nil {
		return nil
	}
	return &bankpb.CardResponse{
		CardId:         fmt.Sprintf("%d", card.Id),
		CardNumber:     card.Number,
		CardType:       string(card.Type),
		CardBrand:      string(card.Brand),
		CreationDate:   card.Creation_date.Format(time.RFC3339),
		ExpirationDate: card.Valid_until.Format(time.RFC3339),
		AccountNumber:  card.Account_number,
		Cvv:            card.Cvv,
		Limit:          card.Card_limit,
		Status:         string(card.Status),
	}
}

func (s *Server) checkCardLimit(userEmail string, accountNumber string) error {
	isAuth, _ := s.IsAuthorizedParty(userEmail, accountNumber)
	limit := 2
	if isAuth {
		limit = 1
	}

	count, err := s.CountActiveCardsByAccountNumber(accountNumber)
	if err != nil {
		return status.Error(codes.Internal, "failed to check limits")
	}

	if count >= limit {
		return status.Error(codes.FailedPrecondition, "card limit reached for this user type")
	}
	return nil
}

func (s *Server) CreateCard(_ context.Context, req *bankpb.CreateCardRequest) (*bankpb.CardResponse, error) {
	_, err := s.GetAccountByNumberRecord(req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}

	if err := s.checkCardLimit(req.Email, req.AccountNumber); err != nil {
		return nil, err
	}

	brand := card_brand(strings.ToLower(req.CardBrand))
	number, err := GenerateCardNumber(brand, req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	card, err := s.CreateCardRecord(Card{
		Number:         number,
		Type:           card_type(strings.ToLower(req.CardType)),
		Brand:          brand,
		Valid_until:    time.Now().AddDate(5, 0, 0),
		Account_number: req.AccountNumber,
		Cvv:            GenerateCVV(),
		Status:         Active,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create card")
	}

	return mapCardToProto(card), nil
}

func (s *Server) RequestCard(ctx context.Context, req *bankpb.RequestCardRequest) (*bankpb.RequestCardResponse, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "metadata missing")
	}

	emails := md.Get("user-email")
	if len(emails) == 0 {
		return nil, status.Error(codes.Unauthenticated, "email missing in metadata")
	}
	userEmail := emails[0]

	acc, err := s.GetAccountByNumberRecord(req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}

	err = s.checkCardLimit(emails[0], req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	token := fmt.Sprintf("tkn-%d-%d", time.Now().UnixNano(), acc.Id)
	cardReq := CardRequest{
		Account_number: req.AccountNumber,
		Type:           card_type(strings.ToLower(req.CardType)),
		Brand:          card_brand(strings.ToLower(req.CardBrand)),
		Token:          token,
		ExpirationDate: time.Now().Add(24 * time.Hour),
		Complete:       false,
		Email:          userEmail,
	}

	_, err = s.CreateCardRequestRecord(cardReq)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create request")
	}

	baseUrl := "http://localhost:8080/api/cards/confirm/?token="
	url := baseUrl + token

	err = s.sendCardConfirmationEmail(ctx, userEmail, url)
	if err != nil {
		return nil, err
	}

	return &bankpb.RequestCardResponse{Accepted: true}, nil
}

func (s *Server) ConfirmCard(ctx context.Context, req *bankpb.ConfirmCardRequest) (*bankpb.ConfirmCardResponse, error) {
	request, err := s.GetCardRequestByToken(req.Token)
	if err != nil {
		return nil, status.Error(codes.NotFound, "invalid or expired token")
	}

	if time.Now().After(request.ExpirationDate) {
		return nil, status.Error(codes.DeadlineExceeded, "token expired")
	}

	cardNumber, _ := GenerateCardNumber(request.Brand, request.Account_number)
	_, err = s.CreateCardRecord(Card{
		Number:         cardNumber,
		Type:           request.Type,
		Brand:          request.Brand,
		Valid_until:    time.Now().AddDate(5, 0, 0),
		Account_number: request.Account_number,
		Cvv:            GenerateCVV(),
		Status:         Active,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create card from request")
	}

	err = s.MarkCardRequestFulfilled(request.Id)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to close request")
	}

	err = s.sendCardCreatedEmail(ctx, request.Email)
	if err != nil {
		return nil, err
	}

	return &bankpb.ConfirmCardResponse{}, nil
}

func (s *Server) GetCards(_ context.Context, _ *bankpb.GetCardsRequest) (*bankpb.GetCardsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (s *Server) BlockCard(_ context.Context, req *bankpb.BlockCardRequest) (*bankpb.BlockCardResponse, error) {
	if req.CardId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid card id")
	}

	err := s.BlockCardRecord(req.CardId)
	if err != nil {
		return &bankpb.BlockCardResponse{Success: false}, status.Error(codes.NotFound, "card not found")
	}

	return &bankpb.BlockCardResponse{Success: true}, nil
}
