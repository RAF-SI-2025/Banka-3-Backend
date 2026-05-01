package model

import userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"

func (client Client) ToProtobuf() *userpb.GetClientResponse {
	return &userpb.GetClientResponse{
		Id:          int64(client.Id),
		FirstName:   client.First_name,
		LastName:    client.Last_name,
		BirthDate:   client.Date_of_birth.Unix(),
		Gender:      client.Gender,
		Email:       client.Email,
		PhoneNumber: client.Phone_number,
		Address:     client.Address,
	}
}

func (emp Employee) ToProtobuf() *userpb.GetEmployeeResponse {
	permissions := make([]string, len(emp.Permissions))
	for i, v := range emp.Permissions {
		permissions[i] = v.Name
	}
	return &userpb.GetEmployeeResponse{
		Id:           int64(emp.Id),
		FirstName:    emp.First_name,
		LastName:     emp.Last_name,
		BirthDate:    emp.Date_of_birth.Unix(),
		Gender:       emp.Gender,
		Email:        emp.Email,
		PhoneNumber:  emp.Phone_number,
		Address:      emp.Address,
		Username:     emp.Username,
		Position:     emp.Position,
		Department:   emp.Department,
		Active:       emp.Active,
		Permissions:  permissions,
		Limit:        emp.Limit,
		UsedLimit:    emp.Used_limit,
		NeedApproval: emp.Need_approval,
	}
}

func (client Client) ToProtobuff() *userpb.Client {
	return &userpb.Client{
		Id:          int64(client.Id),
		FirstName:   client.First_name,
		LastName:    client.Last_name,
		DateOfBirth: client.Date_of_birth.Unix(),
		Gender:      client.Gender,
		Email:       client.Email,
		PhoneNumber: client.Phone_number,
		Address:     client.Address,
	}
}
