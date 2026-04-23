package user

import (
	"time"
)

type (
	Client struct {
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

	Employee struct {
		Id            uint64    `gorm:"column:id;type:bigint;not null;primaryKey"`
		First_name    string    `gorm:"column:first_name;type:varchar(100);not null"`
		Last_name     string    `gorm:"column:last_name;type:varchar(100);not null"`
		Date_of_birth time.Time `gorm:"column:date_of_birth;type:date;not null"`

		Gender        string       `gorm:"column:gender;type:varchar(1);not null"`
		Email         string       `gorm:"column:email;type:varchar(255);unique;not null"`
		Phone_number  string       `gorm:"column:phone_number;type:varchar(20); not null"`
		Address       string       `gorm:"column:address;type:varchar(255);not null"`
		Username      string       `gorm:"column:username;type:varchar(100);unique;not null"`
		Password      []byte       `gorm:"column:password;type:bytea;not null"`
		Salt_password []byte       `gorm:"column:salt_password;type:bytea;not null"`
		Position      string       `gorm:"column:position;type:varchar(100);not null"`
		Department    string       `gorm:"column:department;type:varchar(100);not null"`
		Active        bool         `gorm:"column:active;type:bool; not null"`
		Limit         int64        `gorm:"column:limit;type:bigint;not null;default:0"`
		Used_limit    int64        `gorm:"column:used_limit;type:bigint;not null;default:0"`
		Need_approval bool         `gorm:"column:need_approval;type:bool;not null;default:false"`
		Created_at    time.Time    `gorm:"column:created_at;not null;autoCreateTime"`
		Updated_at    time.Time    `gorm:"column:updated_at;not null;autoUpdateTime"`
		Permissions   []Permission `gorm:"many2many:employee_permissions;joinForeignKey:Employee_id;joinReferences:PermissionId"`
	}

	Permission struct {
		Id   uint64 `gorm:"column:id;type:bigint;not null;primaryKey"`
		Name string `gorm:"column:name;type:varchar(100);not null"`
	}
	EmployeePermissions struct {
		EmployeeId   uint64 `gorm:"column:employee_id;not null"`
		PermissionId uint64 `gorm:"column:permission_id;not null"`
	}
	VerificationCode struct {
		Id             int64     `gorm:"column:id;type:bigserial;not null;primaryKey"`
		Client_id      int64     `gorm:"column:client_id;type:bigint;references clients(id)"`
		Transaction_id int64     `gorm:"column:transaction_id;type:bigint;references payments(transaction_id)"`
		Valid_until    time.Time `gorm:"column:created_at;not null"`
		Tries          int       `gorm:"column:tries;type:int;not null;default 0"`
		Valid          bool      `gorm:"column:valid;type:boolean;not null;default true"`
		Used           bool      `gorm:"column:used;type:boolean;not null;default false"`
	}

	BackupCodes struct {
		ClientId int64  `gorm:"column:client_id;type:bigserial;not null;primaryKey"`
		Token    string `gorm:"column:token;type:varchar(6);not null"`
		Used     bool   `gorm:"column:used;type:boolean;not null; default false"`
	}
)

func (Client) TableName() string {
	return "clients"
}

func (Employee) TableName() string {
	return "employees"
}

func (Permission) TableName() string {
	return "permissions"
}

func (EmployeePermissions) TableName() string {
	return "employee_permissions"
}

func (VerificationCode) TableName() string {
	return "verification_codes"
}

func (BackupCodes) TableName() string {
	return "backup_codes"
}
