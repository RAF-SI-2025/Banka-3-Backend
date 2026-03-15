package user

import (
	"time"
)

type (
	Clients struct {
		Id            uint64    `gorm:"column:id;type:bigint;not null;primaryKey"`
		First_name    string    `gorm:"column:first_name;type:varchar(100);not null"`
		Last_name     string    `gorm:"column:last_name;type:varchar(100);not null"`
		Date_of_birth time.Time `gorm:"column:date_of_birth;type:date;not null"`
		Gender        string    `gorm:"column:gender;type:varchar(1);not null"`
		Email         string    `gorm:"column:email;type:varchar(255);unique;not null"`
		Phone_number  string    `gorm:"column:phone_number;type:varchar(20);not null"`
		Address       string    `gorm:"column:address;type:varchar(255);not null"`
		Password      []byte    `gorm:"column:password;type:bytea;not null"`
		Salt_password []byte    `gorm:"column:salt_password;type:bytea;not null"`
		Created_at    time.Time `gorm:"column:created_at;not null;autoCreateTime"`
		Updated_at    time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
	}

	Employees struct {
		Id            uint64    `gorm:"column:id;type:bigint;not null;primaryKey"`
		First_name    string    `gorm:"column:first_name;type:varchar(100);not null"`
		Last_name     string    `gorm:"column:last_name;type:varchar(100);not null"`
		Date_of_birth time.Time `gorm:"column:date_of_birth;type:date;not null"`

		Gender        string    `gorm:"column:gender;type:varchar(1);not null"`
		Email         string    `gorm:"column:email;type:varchar(255);unique;not null"`
		Phone_number  string    `gorm:"column:phone_number;type:varchar(20); not null"`
		Address       string    `gorm:"column:address;type:varchar(255);not null"`
		Username      string    `gorm:"column:username;type:varchar(100);unique;not null"`
		Password      []byte    `gorm:"column:password;type:bytea;not null"`
		Salt_password []byte    `gorm:"column:salt_password;type:bytea;not null"`
		Position      string    `gorm:"column:position;type:varchar(100);not null"`
		Department    string    `gorm:"column:department;type:varchar(100);not null"`
		Active        bool      `gorm:"column:active;type:bool; not null"`
		Created_at    time.Time `gorm:"column:created_at;not null;autoCreateTime"`
		Updated_at    time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
	}

	Permissions struct {
		Id   uint64 `gorm:"column:id;type:bigint;not null;primaryKey"`
		Name string `gorm:"column:name;type:varchar(100);not null"`
	}
	EmployeePermissions struct {
		Employee_id  uint64 `gorm:"column:employee_id;not null"`
		PermissionId uint64 `gorm:"column:permission_id;not null"`
	}

	Employee_by_Id_response struct {
		Id              int       `gorm:"column:id;type:bigint;not null;primaryKey"`
		First_name      string    `gorm:"column:first_name;type:varchar(100);not null"`
		Last_name       string    `gorm:"column:last_name;type:varchar(100);not null"`
		Date_of_birth   time.Time `gorm:"column:date_of_birth;type:date;not null"`
		Gender          string    `gorm:"column:gender;type:varchar(1);not null"`
		Email           string    `gorm:"column:email;type:varchar(255);unique;not null"`
		Phone_number    string    `gorm:"column:phone_number;type:varchar(20);not null"`
		Address         string    `gorm:"column:address;type:varchar(255);not null"`
		Username        string    `gorm:"column:address;type:varchar(100);not null"`
		Position        string    `gorm:"column:position;type:varchar(100);not null"`
		Active          bool      `gorm:"column:active;type:bool; not null"`
		Department      string    `gorm:"column:department;type:varchar(100);not null"`
		Permission_id   int64     `gorm:"column:Permission_id;type:bigint;not null;primaryKey"`
		Permission_name string    `gorm:"column:Permission_name;type:varchar(100);not null"`
	}
	Get_employees struct {
		Id              int    `gorm:"column:id;type:bigint;not null;primaryKey"`
		First_name      string `gorm:"column:first_name;type:varchar(100);not null"`
		Last_name       string `gorm:"column:last_name;type:varchar(100);not null"`
		Email           string `gorm:"column:email;type:varchar(255);unique;not null"`
		Position        string `gorm:"column:position;type:varchar(100);not null"`
		Phone_number    string `gorm:"column:phone_number;type:varchar(20);not null"`
		Active          bool   `gorm:"column:active;type:bool; not null"`
		Permission_id   int64  `gorm:"column:Permission_id;type:bigint;not null;primaryKey"`
		Permission_name string `gorm:"column:Permission_name;type:varchar(100);not null"`
	}
)

func (Clients) TableName() string {
	return "clients"
}

func (Employees) TableName() string {
	return "employees"
}

func (Employee_by_Id_response) TableName() string {
	return "EmployeeAndPerms"
}
