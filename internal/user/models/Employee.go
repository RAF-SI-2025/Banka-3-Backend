package models

import "time"

type Employee struct {
	Id           uint64       `gorm:"column:id;type:bigint;not null;primaryKey"`
	FirstName    string       `gorm:"column:first_name;type:varchar(100);not null"`
	LastName     string       `gorm:"column:last_name;type:varchar(100);not null"`
	DateOfBirth  time.Time    `gorm:"column:date_of_birth;type:date;not null"`
	Gender       string       `gorm:"column:gender;type:varchar(1);not null"`
	Email        string       `gorm:"column:email;type:varchar(255);unique;not null"`
	PhoneNumber  string       `gorm:"column:phone_number;type:varchar(20);not null"`
	Address      string       `gorm:"column:address;type:varchar(255);not null"`
	Username     string       `gorm:"column:username;type:varchar(100);unique;not null"`
	Password     []byte       `gorm:"column:password;type:bytea;not null"`
	SaltPassword []byte       `gorm:"column:salt_password;type:bytea;not null"`
	Position     string       `gorm:"column:position;type:varchar(100);not null"`
	Department   string       `gorm:"column:department;type:varchar(100);not null"`
	Active       bool         `gorm:"column:active;type:boolean;not null"`
	Permissions  []Permission `gorm:"many2many:employee_permissions;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
	CreatedAt    time.Time    `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt    time.Time    `gorm:"column:updated_at;not null;autoUpdateTime"`
}
