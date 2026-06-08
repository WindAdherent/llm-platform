package domain

import "time"

type Task struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	TaskType     string     `gorm:"size:64;not null;index" json:"task_type"`
	Status       string     `gorm:"size:32;not null;index" json:"status"`
	Progress     int        `gorm:"not null;default:0" json:"progress"`
	Message      string     `gorm:"size:1024" json:"message"`
	ErrorMessage string     `gorm:"size:2048" json:"error_message"`
	ResultJSON   *string    `gorm:"type:json" json:"result_json,omitempty"`
	CreatedBy    string     `gorm:"size:128" json:"created_by"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
