package database

import (
	"gorm.io/gorm"

	"github.com/WindAdherent/llm-platform/internal/domain"
)

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&domain.Model{},
		&domain.ModelVersion{},
	)
}
