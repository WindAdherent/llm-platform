package database

import (
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/WindAdherent/llm-platform/internal/config"
)

func ConnectMySQL(cfg config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.MySQLDSN()), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}
