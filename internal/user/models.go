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
		Phone_number  string    `gorm:"column:phone_number;type:varchar(20); not null"`
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
		Password      []byte    `gorm:"column:password:type:bytea:not null"`
		Salt_password []byte    `gorm:"column:salt_password;type:bytea;not null"`
		Position      string    `gorm:"column:position;type:varchar(100);not null"`
		Department    string    `gorm:"column:department;type:varchar(100);not null"`
		Active        bool      `gorm:"column:active;type:bool; not null"`
		Created_at    time.Time `gorm:"column:created_at;not null;autoCreateTime"`
		Updated_at    time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
	}
)

func (Clients) TableName() string {
	return "clients"
}

func (Employees) TableName() string {
	return "employees"
}
