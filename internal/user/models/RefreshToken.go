package models

import "time"

type RefreshToken struct {
	Id         uint64    `gorm:"column:id;type:bigint;not null;primaryKey"`
	EmployeeId uint64    `gorm:"column:employee_id;type:bigint;not null"`
	Token      string    `gorm:"column:token;type:varchar(512);unique;not null"`
	ExpiresAt  time.Time `gorm:"column:expires_at;not null"`
	Revoked    bool      `gorm:"column:revoked;type:boolean;not null;default:false"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime"`
	Employee   Employee  `gorm:"foreignKey:EmployeeId"`
}
