package models

import "gorm.io/gorm"

type Client struct {
	gorm.Model
	FirstName         string `gorm:"not null" json:"first_name"`
	LastName          string `gorm:"not null" json:"last_name"`
	DateOfBirth       int64  `gorm:"not null" json:"date_of_birth"`
	Gender            string `gorm:"type:varchar(1)" json:"gender"`
	Email             string `gorm:"uniqueIndex;not null" json:"email"`
	PhoneNumber       string `gorm:"not null" json:"phone_number"`
	Address           string `gorm:"not null" json:"address"`
	Password          string `gorm:"not null" json:"-"`
	SaltPassword      string `gorm:"not null" json:"-"`
	ConnectedAccounts string `gorm:"type:text" json:"connected_accounts"`
}
