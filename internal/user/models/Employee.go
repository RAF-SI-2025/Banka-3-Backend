package models

import (
	"time"

	"gorm.io/gorm"
)

type Employee struct {
	gorm.Model
	FirstName    string       `gorm:"not null" json:"first_name"`
	LastName     string       `gorm:"not null" json:"last_name"`
	DateOfBirth  time.Time    `gorm:"type:date" json:"date_of_birth"`
	Gender       string       `gorm:"type:varchar(1)" json:"gender"`
	Email        string       `gorm:"uniqueIndex;not null" json:"email"`
	PhoneNumber  string       `gorm:"not null" json:"phone_number"`
	Address      string       `gorm:"not null" json:"address"`
	Username     string       `gorm:"uniqueIndex;not null" json:"username"`
	Password     string       `gorm:"not null" json:"-"`
	SaltPassword string       `gorm:"not null" json:"-"`
	Position     string       `gorm:"not null" json:"position"`
	Department   string       `gorm:"not null" json:"department"`
	IsActive     bool         `gorm:"default:true" json:"is_active"`
	Permissions  []Permission `gorm:"many2many:employee_permissions;" json:"permissions"`
}
