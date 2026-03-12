package models

import "time"

type Permission struct {
	Id        uint64    `gorm:"column:id;type:bigint;not null;primaryKey"`
	Name      string    `gorm:"column:name;type:varchar(100);unique;not null" json:"name"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime"`
}
