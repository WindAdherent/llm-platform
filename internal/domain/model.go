package domain

import "time"

type Model struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Family      string    `gorm:"size:64;not null" json:"family"`
	SourceType  string    `gorm:"size:32;not null" json:"source_type"`
	SourceURI   string    `gorm:"size:512;not null" json:"source_uri"`
	Description string    `gorm:"size:1024" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	Versions []ModelVersion `gorm:"foreignKey:ModelID" json:"versions,omitempty"`
}

type ModelVersion struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	ModelID       uint      `gorm:"not null;index" json:"model_id"`
	VersionName   string    `gorm:"size:128;not null" json:"version_name"`
	Revision      string    `gorm:"size:128;not null;default:main" json:"revision"`
	LocalPath     string    `gorm:"size:512" json:"local_path"`
	ParameterSize string    `gorm:"size:32" json:"parameter_size"`
	Precision     string    `gorm:"size:32" json:"precision"`
	Status        string    `gorm:"size:32;not null;default:CREATED" json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
