package server

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/model"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func (s *Server) GetClients(ctx context.Context, req *userpb.GetClientsRequest) (*userpb.GetClientsResponse, error) {

	clients, err := s.repo.GetAllClients(repo.UserRestrictions{"first_name": strings.TrimSpace(req.FirstName), "last_name": strings.TrimSpace(req.LastName), "email": strings.TrimSpace(req.Email)})

	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "error retrieving clients", "err", err)
		return nil, status.Error(codes.Internal, "Failed to retrieve clients")
	}

	var clientResponses []*userpb.Client
	for _, client := range clients {
		clientResponses = append(clientResponses, client.ToProtobuff())
	}

	return &userpb.GetClientsResponse{Clients: clientResponses}, nil
}

func (s *Server) UpdateClient(ctx context.Context, req *userpb.UpdateClientRequest) (*userpb.UpdateClientResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than zero")
	}
	if strings.TrimSpace(req.Gender) != "" && req.Gender != "M" && req.Gender != "F" {
		return nil, status.Error(codes.InvalidArgument, "Gender must be one of M or F")
	}
	client := model.Client{
		Id:           uint64(req.Id),
		First_name:   req.FirstName,
		Last_name:    req.LastName,
		Gender:       req.Gender,
		Email:        req.Email,
		Phone_number: req.PhoneNumber,
		Address:      req.Address,
	}

	// I hope any potential reader of this has as much fun reading it as I had Implementing it.
	ref := reflect.ValueOf(&client).Elem()
	for i := 0; i < ref.NumField(); i++ {
		field := ref.Field(i)
		if field.Type() == reflect.TypeFor[string]() {
			if !field.CanSet() {
				logger.FromContext(ctx).ErrorContext(ctx, "cannot set the value of struct field")
				// This need not be an error, but it will also probably
				// never happen
				return nil, status.Error(codes.Internal, "client update failed")
			}
			field.SetString(strings.TrimSpace(field.String()))
		}

	}

	if req.DateOfBirth != 0 {
		client.Date_of_birth = time.Unix(req.DateOfBirth, 0)
	}

	_, err := s.repo.UpdateClient(client)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrClientNotFound):
			return nil, status.Error(codes.NotFound, "client not found")
		case errors.Is(err, repo.ErrClientEmailExists):
			return nil, status.Error(codes.AlreadyExists, "client with that email already exists")
		case errors.Is(err, repo.ErrClientNoFieldsToUpdate):
			return nil, status.Error(codes.InvalidArgument, "no fields to update")
		default:
			return nil, status.Error(codes.Internal, "client update failed")
		}
	}

	return &userpb.UpdateClientResponse{Valid: true, Response: "Client updated"}, nil
}

func (s *Server) CreateClientAccount(ctx context.Context, req *userpb.CreateClientRequest) (*userpb.CreateClientResponse, error) {
	is_null := func(str string) bool {
		return strings.TrimSpace(str) == ""
	}
	vals := []string{req.FirstName, req.LastName, req.Gender, req.Email, req.PhoneNumber,
		req.Address}

	if slices.ContainsFunc(vals, is_null) {
		return nil, status.Error(codes.InvalidArgument, "One of the required cols is null")
	}

	if req.Gender != "M" && req.Gender != "F" {
		return nil, status.Error(codes.InvalidArgument, "Gender must be one of M or F")
	}

	salt, salt_err := generateSalt()
	if salt_err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "error generating salt", "err", salt_err)
		return nil, status.Error(codes.Internal, "Password salting failed")
	}

	client := model.Client{First_name: req.FirstName,
		Last_name: req.LastName, Date_of_birth: time.Unix(req.BirthDate, 0),
		Gender: req.Gender, Email: req.Email, Phone_number: req.PhoneNumber,
		Address: req.Address, Password: HashPassword(req.Password, salt),
		Salt_password: salt}

	err := s.repo.CreateClient(client)
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "client creation failed", "err", err)
		return nil, status.Error(codes.Internal, "Client creation failed")
	}
	return &userpb.CreateClientResponse{Valid: true}, nil

}

func (s *Server) GetClientByEmail(_ context.Context, req *userpb.GetUserByEmailRequest) (*userpb.GetClientResponse, error) {
	resp, err := s.repo.GetClientByAttribute("email", req.Email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "employee not found")
		}
		return nil, status.Error(codes.Internal, "failed to get employee")
	}
	return resp.ToProtobuf(), nil
}

func (s *Server) GetClientById(_ context.Context, req *userpb.GetUserByIdRequest) (*userpb.GetClientResponse, error) {
	resp, err := s.repo.GetClientByAttribute("id", req.Id)
	if err != nil {
		return nil, err
	}
	return resp.ToProtobuf(), nil
}
