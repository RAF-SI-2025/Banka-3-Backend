package models

import "gorm.io/gorm"

type Client struct {
	gorm.Model
	FirstName   string `gorm:"not null"`
	LastName    string `gorm:"not null"`
	DateOfBirth int64  `gorm:"not null"`
	Gender      string `gorm:"not null"`
	Email       string `gorm:"unique;not null"`
	PhoneNumber string
	Address     string
	Password    string   `gorm:"not null"`
	Salt        string   `gorm:"not null"`
	Accounts    []string `gorm:"type:text[]"`
}
