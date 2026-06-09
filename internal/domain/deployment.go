package domain

import "time"

type Deployment struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Name            string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	ModelVersionID  uint      `gorm:"not null;index" json:"model_version_id"`
	RuntimeType     string    `gorm:"size:32;not null;index" json:"runtime_type"`
	AcceleratorType string    `gorm:"size:32;not null;index" json:"accelerator_type"`
	Status          string    `gorm:"size:32;not null;index" json:"status"`
	Replicas        int       `gorm:"not null;default:1" json:"replicas"`
	Endpoint        string    `gorm:"size:512" json:"endpoint"`
	Description     string    `gorm:"size:1024" json:"description"`
	CreatedBy       string    `gorm:"size:128" json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	ModelVersion  ModelVersion            `gorm:"foreignKey:ModelVersionID" json:"model_version,omitempty"`
	RuntimeConfig DeploymentRuntimeConfig `gorm:"foreignKey:DeploymentID" json:"runtime_config,omitempty"`
}

type DeploymentRuntimeConfig struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	DeploymentID uint   `gorm:"not null;uniqueIndex" json:"deployment_id"`
	Image        string `gorm:"size:256;not null" json:"image"`

	ContainerPort int  `gorm:"not null;default:8000" json:"container_port"`
	ServicePort   int  `gorm:"not null;default:8000" json:"service_port"`
	HostPort      *int `gorm:"default:null" json:"host_port,omitempty"`

	TensorParallelSize   int     `gorm:"not null;default:1" json:"tensor_parallel_size"`
	PipelineParallelSize int     `gorm:"not null;default:1" json:"pipeline_parallel_size"`
	GPUMemoryUtilization float64 `gorm:"not null;default:0.9" json:"gpu_memory_utilization"`
	MaxModelLen          int     `gorm:"not null;default:8192" json:"max_model_len"`
	DType                string  `gorm:"size:32;not null;default:auto" json:"dtype"`
	Quantization         string  `gorm:"size:64" json:"quantization"`
	MaxNumSeqs           int     `gorm:"not null;default:0" json:"max_num_seqs"`
	MaxNumBatchedTokens  int     `gorm:"not null;default:0" json:"max_num_batched_tokens"`
	ExtraArgsJSON        *string `gorm:"type:json" json:"extra_args_json,omitempty"`

	RequiredDeviceCount int `gorm:"-" json:"required_device_count"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
