package models

import (
	"time"

	"gorm.io/gorm"
)

type Employee struct {
	gorm.Model
	FirstName   string    `gorm:"not null"`
	LastName    string    `gorm:"not null"`
	DateOfBirth time.Time `gorm:"not null"`
	Gender      string    `gorm:"not null"`
	Email       string    `gorm:"unique;not null"`
	PhoneNumber string
	Address     string
	Username    string `gorm:"unique;not null"`
	Password    string `gorm:"not null"`
	Salt        string `gorm:"not null"`
	Position    string
	Department  string
	Active      bool         `gorm:"default:true"`
	Permissions []Permission `gorm:"many2many:employee_permissions;"`
}
