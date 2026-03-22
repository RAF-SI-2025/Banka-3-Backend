package bank

import (
	"context"
	"errors"
	"strings"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func mapCompanyToProto(company *Company) *bankpb.Company {
	if company == nil {
		return nil
	}

	return &bankpb.Company{
		Id:             company.Id,
		RegisteredId:   company.Registered_id,
		Name:           company.Name,
		TaxCode:        company.Tax_code,
		ActivityCodeId: company.Activity_code_id,
		Address:        company.Address,
		OwnerId:        company.Owner_id,
	}
}

func validateCreateCompanyInput(registeredID int64, name string, taxCode int64, address string, ownerID int64) error {
	if registeredID <= 0 {
		return status.Error(codes.InvalidArgument, "registered id must be greater than zero")
	}
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}
	if taxCode <= 0 {
		return status.Error(codes.InvalidArgument, "tax code must be greater than zero")
	}
	if strings.TrimSpace(address) == "" {
		return status.Error(codes.InvalidArgument, "address is required")
	}
	if ownerID <= 0 {
		return status.Error(codes.InvalidArgument, "owner id must be greater than zero")
	}
	return nil
}

func validateUpdateCompanyInput(id int64, name string, address string, ownerID int64) error {
	if id <= 0 {
		return status.Error(codes.InvalidArgument, "id must be greater than zero")
	}
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}
	if strings.TrimSpace(address) == "" {
		return status.Error(codes.InvalidArgument, "address is required")
	}
	if ownerID <= 0 {
		return status.Error(codes.InvalidArgument, "owner id must be greater than zero")
	}
	return nil
}

func (s *Server) CreateCompany(_ context.Context, req *bankpb.CreateCompanyRequest) (*bankpb.CreateCompanyResponse, error) {
	if err := validateCreateCompanyInput(req.RegisteredId, req.Name, req.TaxCode, req.Address, req.OwnerId); err != nil {
		return nil, err
	}

	company, err := s.CreateCompanyRecord(Company{
		Registered_id:    req.RegisteredId,
		Name:             strings.TrimSpace(req.Name),
		Tax_code:         req.TaxCode,
		Activity_code_id: req.ActivityCodeId,
		Address:          strings.TrimSpace(req.Address),
		Owner_id:         req.OwnerId,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyRegisteredIDExists):
			return nil, status.Error(codes.AlreadyExists, "company with that registered id already exists")
		case errors.Is(err, ErrCompanyOwnerNotFound):
			return nil, status.Error(codes.InvalidArgument, "owner does not exist")
		case errors.Is(err, ErrCompanyActivityCodeNotFound):
			return nil, status.Error(codes.InvalidArgument, "activity code does not exist")
		default:
			return nil, status.Error(codes.Internal, "company creation failed")
		}
	}

	return &bankpb.CreateCompanyResponse{Company: mapCompanyToProto(company)}, nil
}

func (s *Server) GetCompanyById(_ context.Context, req *bankpb.GetCompanyByIdRequest) (*bankpb.GetCompanyByIdResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than zero")
	}

	company, err := s.GetCompanyByIDRecord(req.Id)
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyNotFound):
			return nil, status.Error(codes.NotFound, "company not found")
		default:
			return nil, status.Error(codes.Internal, "company lookup failed")
		}
	}

	return &bankpb.GetCompanyByIdResponse{Company: mapCompanyToProto(company)}, nil
}

func (s *Server) GetCompanies(_ context.Context, _ *bankpb.GetCompaniesRequest) (*bankpb.GetCompaniesResponse, error) {
	companies, err := s.GetCompaniesRecords()
	if err != nil {
		return nil, status.Error(codes.Internal, "company listing failed")
	}

	var responseCompanies []*bankpb.Company
	for _, company := range companies {
		responseCompanies = append(responseCompanies, mapCompanyToProto(company))
	}

	return &bankpb.GetCompaniesResponse{Companies: responseCompanies}, nil
}

func (s *Server) UpdateCompany(_ context.Context, req *bankpb.UpdateCompanyRequest) (*bankpb.UpdateCompanyResponse, error) {
	if err := validateUpdateCompanyInput(req.Id, req.Name, req.Address, req.OwnerId); err != nil {
		return nil, err
	}

	company, err := s.UpdateCompanyRecord(Company{
		Id:               req.Id,
		Name:             strings.TrimSpace(req.Name),
		Activity_code_id: req.ActivityCodeId,
		Address:          strings.TrimSpace(req.Address),
		Owner_id:         req.OwnerId,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyNotFound):
			return nil, status.Error(codes.NotFound, "company not found")
		case errors.Is(err, ErrCompanyOwnerNotFound):
			return nil, status.Error(codes.InvalidArgument, "owner does not exist")
		case errors.Is(err, ErrCompanyActivityCodeNotFound):
			return nil, status.Error(codes.InvalidArgument, "activity code does not exist")
		default:
			return nil, status.Error(codes.Internal, "company update failed")
		}
	}

	return &bankpb.UpdateCompanyResponse{Company: mapCompanyToProto(company)}, nil
}
