package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/WindAdherent/llm-platform/internal/domain"
)

type DeploymentHandler struct {
	db *gorm.DB
}

func NewDeploymentHandler(db *gorm.DB) *DeploymentHandler {
	return &DeploymentHandler{db: db}
}

type CreateDeploymentRequest struct {
	Name            string `json:"name" binding:"required"`
	ModelVersionID  uint   `json:"model_version_id" binding:"required"`
	RuntimeType     string `json:"runtime_type"`
	AcceleratorType string `json:"accelerator_type"`
	Replicas        int    `json:"replicas"`
	Description     string `json:"description"`

	Image string `json:"image"`
	Port  int    `json:"port"`

	ContainerPort int  `json:"container_port"`
	ServicePort   int  `json:"service_port"`
	HostPort      *int `json:"host_port"`

	TensorParallelSize   int                    `json:"tensor_parallel_size"`
	PipelineParallelSize int                    `json:"pipeline_parallel_size"`
	GPUMemoryUtilization float64                `json:"gpu_memory_utilization"`
	MaxModelLen          int                    `json:"max_model_len"`
	DType                string                 `json:"dtype"`
	Quantization         string                 `json:"quantization"`
	MaxNumSeqs           int                    `json:"max_num_seqs"`
	MaxNumBatchedTokens  int                    `json:"max_num_batched_tokens"`
	ExtraArgs            map[string]interface{} `json:"extra_args"`
}

func (h *DeploymentHandler) CreateDeployment(c *gin.Context) {
	var req CreateDeploymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request body",
			"details": err.Error(),
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "deployment name is required",
		})
		return
	}

	normalizeCreateDeploymentRequest(&req)

	if err := validateCreateDeploymentRequest(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	if !isSupportedRuntimeType(req.RuntimeType) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "unsupported runtime_type",
			"supported_runtime_types": []string{
				"vllm",
				"tensorrt_llm",
			},
		})
		return
	}

	if !isSupportedAcceleratorType(req.AcceleratorType) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "unsupported accelerator_type",
			"supported_accelerator_types": []string{
				"nvidia",
				"hygon_dcu",
			},
		})
		return
	}

	var modelVersion domain.ModelVersion
	if err := h.db.First(&modelVersion, req.ModelVersionID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "model version not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get model version",
			"details": err.Error(),
		})
		return
	}

	if modelVersion.LocalPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "model version local_path is empty, please download or prepare the model first",
		})
		return
	}

	if modelVersion.Status != "READY" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":                "model version is not ready",
			"model_version_status": modelVersion.Status,
		})
		return
	}

	extraArgsJSON := mapToJSONStringPtr(req.ExtraArgs)

	deployment := domain.Deployment{
		Name:            req.Name,
		ModelVersionID:  req.ModelVersionID,
		RuntimeType:     req.RuntimeType,
		AcceleratorType: req.AcceleratorType,
		Status:          "CREATED",
		Replicas:        req.Replicas,
		Description:     req.Description,
	}

	runtimeConfig := domain.DeploymentRuntimeConfig{
		Image:                req.Image,
		ContainerPort:        req.ContainerPort,
		ServicePort:          req.ServicePort,
		HostPort:             req.HostPort,
		TensorParallelSize:   req.TensorParallelSize,
		PipelineParallelSize: req.PipelineParallelSize,
		GPUMemoryUtilization: req.GPUMemoryUtilization,
		MaxModelLen:          req.MaxModelLen,
		DType:                req.DType,
		Quantization:         req.Quantization,
		MaxNumSeqs:           req.MaxNumSeqs,
		MaxNumBatchedTokens:  req.MaxNumBatchedTokens,
		ExtraArgsJSON:        extraArgsJSON,
	}

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&deployment).Error; err != nil {
			return err
		}

		runtimeConfig.DeploymentID = deployment.ID

		if err := tx.Create(&runtimeConfig).Error; err != nil {
			return err
		}

		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create deployment",
			"details": err.Error(),
		})
		return
	}

	if err := h.db.
		Preload("ModelVersion").
		Preload("RuntimeConfig").
		First(&deployment, deployment.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to reload deployment",
			"details": err.Error(),
		})
		return
	}

	decorateDeployment(&deployment)

	c.JSON(http.StatusCreated, deployment)
}

func (h *DeploymentHandler) ListDeployments(c *gin.Context) {
	var deployments []domain.Deployment

	if err := h.db.
		Preload("ModelVersion").
		Preload("RuntimeConfig").
		Order("id desc").
		Find(&deployments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to list deployments",
			"details": err.Error(),
		})
		return
	}

	for i := range deployments {
		decorateDeployment(&deployments[i])
	}

	c.JSON(http.StatusOK, gin.H{
		"items": deployments,
		"total": len(deployments),
	})
}

func (h *DeploymentHandler) GetDeployment(c *gin.Context) {
	deploymentID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid deployment id",
		})
		return
	}

	var deployment domain.Deployment
	if err := h.db.
		Preload("ModelVersion").
		Preload("RuntimeConfig").
		First(&deployment, deploymentID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "deployment not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get deployment",
			"details": err.Error(),
		})
		return
	}

	decorateDeployment(&deployment)

	c.JSON(http.StatusOK, deployment)
}

type UpdateDeploymentConfigRequest struct {
	Replicas             *int                   `json:"replicas"`
	Description          *string                `json:"description"`
	Image                *string                `json:"image"`
	Port                 *int                   `json:"port"`
	ContainerPort        *int                   `json:"container_port"`
	ServicePort          *int                   `json:"service_port"`
	HostPort             *int                   `json:"host_port"`
	TensorParallelSize   *int                   `json:"tensor_parallel_size"`
	PipelineParallelSize *int                   `json:"pipeline_parallel_size"`
	GPUMemoryUtilization *float64               `json:"gpu_memory_utilization"`
	MaxModelLen          *int                   `json:"max_model_len"`
	DType                *string                `json:"dtype"`
	Quantization         *string                `json:"quantization"`
	MaxNumSeqs           *int                   `json:"max_num_seqs"`
	MaxNumBatchedTokens  *int                   `json:"max_num_batched_tokens"`
	ExtraArgs            map[string]interface{} `json:"extra_args"`
}

func (h *DeploymentHandler) UpdateDeploymentConfig(c *gin.Context) {
	deploymentID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid deployment id",
		})
		return
	}

	var req UpdateDeploymentConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request body",
			"details": err.Error(),
		})
		return
	}

	var deployment domain.Deployment
	if err := h.db.
		Preload("RuntimeConfig").
		First(&deployment, deploymentID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "deployment not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get deployment",
			"details": err.Error(),
		})
		return
	}

	if deployment.Status == "RUNNING" || deployment.Status == "STARTING" || deployment.Status == "STOPPING" {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "deployment config cannot be updated while deployment is active",
			"status": deployment.Status,
		})
		return
	}

	deploymentUpdates := map[string]interface{}{}
	configUpdates := map[string]interface{}{}

	if req.Replicas != nil {
		if *req.Replicas <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "replicas must be greater than 0",
			})
			return
		}
		deploymentUpdates["replicas"] = *req.Replicas
	}

	if req.Description != nil {
		deploymentUpdates["description"] = *req.Description
	}

	if req.Image != nil {
		image := strings.TrimSpace(*req.Image)
		if image == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "image cannot be empty",
			})
			return
		}
		configUpdates["image"] = image
	}

	if req.Port != nil {
		req.ContainerPort = req.Port
	}

	if req.ContainerPort != nil {
		if *req.ContainerPort <= 0 || *req.ContainerPort > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "container_port must be between 1 and 65535",
			})
			return
		}
		configUpdates["container_port"] = *req.ContainerPort
	}

	if req.ServicePort != nil {
		if *req.ServicePort <= 0 || *req.ServicePort > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "service_port must be between 1 and 65535",
			})
			return
		}
		configUpdates["service_port"] = *req.ServicePort
	}

	if req.HostPort != nil {
		if *req.HostPort <= 0 || *req.HostPort > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "host_port must be between 1 and 65535",
			})
			return
		}
		configUpdates["host_port"] = *req.HostPort
	}

	if req.TensorParallelSize != nil {
		if *req.TensorParallelSize <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "tensor_parallel_size must be greater than 0",
			})
			return
		}
		configUpdates["tensor_parallel_size"] = *req.TensorParallelSize
	}

	if req.PipelineParallelSize != nil {
		if *req.PipelineParallelSize <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "pipeline_parallel_size must be greater than 0",
			})
			return
		}
		configUpdates["pipeline_parallel_size"] = *req.PipelineParallelSize
	}

	if req.GPUMemoryUtilization != nil {
		if *req.GPUMemoryUtilization <= 0 || *req.GPUMemoryUtilization > 1 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "gpu_memory_utilization must be in (0, 1]",
			})
			return
		}
		configUpdates["gpu_memory_utilization"] = *req.GPUMemoryUtilization
	}

	if req.MaxModelLen != nil {
		if *req.MaxModelLen <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "max_model_len must be greater than 0",
			})
			return
		}
		configUpdates["max_model_len"] = *req.MaxModelLen
	}

	if req.DType != nil {
		configUpdates["dtype"] = normalizeStringDefault(*req.DType, "auto")
	}

	if req.Quantization != nil {
		configUpdates["quantization"] = strings.TrimSpace(*req.Quantization)
	}

	if req.MaxNumSeqs != nil {
		if *req.MaxNumSeqs < 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "max_num_seqs cannot be negative",
			})
			return
		}
		configUpdates["max_num_seqs"] = *req.MaxNumSeqs
	}

	if req.MaxNumBatchedTokens != nil {
		if *req.MaxNumBatchedTokens < 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "max_num_batched_tokens cannot be negative",
			})
			return
		}
		configUpdates["max_num_batched_tokens"] = *req.MaxNumBatchedTokens
	}

	if req.ExtraArgs != nil {
		configUpdates["extra_args_json"] = mapToJSONStringPtr(req.ExtraArgs)
	}

	if len(deploymentUpdates) == 0 && len(configUpdates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "no fields to update",
		})
		return
	}

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if len(deploymentUpdates) > 0 {
			if err := tx.Model(&deployment).Updates(deploymentUpdates).Error; err != nil {
				return err
			}
		}

		if len(configUpdates) > 0 {
			if err := tx.Model(&domain.DeploymentRuntimeConfig{}).
				Where("deployment_id = ?", deployment.ID).
				Updates(configUpdates).Error; err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to update deployment config",
			"details": err.Error(),
		})
		return
	}

	if err := h.db.
		Preload("ModelVersion").
		Preload("RuntimeConfig").
		First(&deployment, deployment.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to reload deployment",
			"details": err.Error(),
		})
		return
	}

	decorateDeployment(&deployment)

	c.JSON(http.StatusOK, deployment)
}

func (h *DeploymentHandler) GetRuntimeCommand(c *gin.Context) {
	deploymentID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid deployment id",
		})
		return
	}

	var deployment domain.Deployment
	if err := h.db.
		Preload("ModelVersion").
		Preload("RuntimeConfig").
		First(&deployment, deploymentID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "deployment not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get deployment",
			"details": err.Error(),
		})
		return
	}

	if deployment.RuntimeType != "vllm" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":        "runtime command preview currently supports vllm only",
			"runtime_type": deployment.RuntimeType,
		})
		return
	}

	command, shellCommand := buildVLLMCommand(deployment)

	decorateDeployment(&deployment)

	c.JSON(http.StatusOK, gin.H{
		"deployment_id":         deployment.ID,
		"runtime_type":          deployment.RuntimeType,
		"accelerator_type":      deployment.AcceleratorType,
		"container_port":        deployment.RuntimeConfig.ContainerPort,
		"service_port":          deployment.RuntimeConfig.ServicePort,
		"host_port":             deployment.RuntimeConfig.HostPort,
		"required_device_count": deployment.RuntimeConfig.RequiredDeviceCount,
		"command":               command,
		"shell":                 shellCommand,
	})
}

func normalizeCreateDeploymentRequest(req *CreateDeploymentRequest) {
	req.RuntimeType = normalizeStringDefault(req.RuntimeType, "vllm")
	req.AcceleratorType = normalizeStringDefault(req.AcceleratorType, "nvidia")

	if req.Replicas <= 0 {
		req.Replicas = 1
	}

	if req.Image == "" {
		switch req.RuntimeType {
		case "vllm":
			if req.AcceleratorType == "hygon_dcu" {
				req.Image = "your-registry/vllm-hygon-dtk:latest"
			} else {
				req.Image = "vllm/vllm-openai:latest"
			}
		case "tensorrt_llm":
			req.Image = "nvcr.io/nvidia/tensorrt-llm/release:latest"
		}
	}

	// Deprecated alias: port -> container_port
	if req.ContainerPort == 0 && req.Port != 0 {
		req.ContainerPort = req.Port
	}

	if req.ContainerPort == 0 {
		req.ContainerPort = 8000
	}

	if req.ServicePort == 0 {
		req.ServicePort = req.ContainerPort
	}

	if req.HostPort != nil {
		if *req.HostPort <= 0 || *req.HostPort > 65535 {
			// 这里不直接返回 error，因为 normalize 函数没有返回值。
			// 严格校验放在 validateCreateDeploymentRequest 里。
		}
	}

	if req.TensorParallelSize == 0 {
		req.TensorParallelSize = 1
	}

	if req.PipelineParallelSize == 0 {
		req.PipelineParallelSize = 1
	}

	if req.GPUMemoryUtilization == 0 {
		req.GPUMemoryUtilization = 0.9
	}

	if req.MaxModelLen == 0 {
		req.MaxModelLen = 8192
	}

	req.DType = normalizeStringDefault(req.DType, "auto")
}

func validateCreateDeploymentRequest(req CreateDeploymentRequest) error {
	if req.ContainerPort <= 0 || req.ContainerPort > 65535 {
		return fmt.Errorf("container_port must be between 1 and 65535")
	}

	if req.ServicePort <= 0 || req.ServicePort > 65535 {
		return fmt.Errorf("service_port must be between 1 and 65535")
	}

	if req.HostPort != nil {
		if *req.HostPort <= 0 || *req.HostPort > 65535 {
			return fmt.Errorf("host_port must be between 1 and 65535")
		}
	}

	if req.TensorParallelSize <= 0 {
		return fmt.Errorf("tensor_parallel_size must be greater than 0")
	}

	if req.PipelineParallelSize <= 0 {
		return fmt.Errorf("pipeline_parallel_size must be greater than 0")
	}

	if req.GPUMemoryUtilization <= 0 || req.GPUMemoryUtilization > 1 {
		return fmt.Errorf("gpu_memory_utilization must be in (0, 1]")
	}

	if req.MaxModelLen <= 0 {
		return fmt.Errorf("max_model_len must be greater than 0")
	}

	if req.Replicas <= 0 {
		return fmt.Errorf("replicas must be greater than 0")
	}

	return nil
}

func buildVLLMCommand(deployment domain.Deployment) ([]string, string) {
	cfg := deployment.RuntimeConfig
	modelPath := deployment.ModelVersion.LocalPath

	command := []string{
		"vllm",
		"serve",
		modelPath,
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", cfg.ContainerPort),
		"--tensor-parallel-size", fmt.Sprintf("%d", cfg.TensorParallelSize),
		"--pipeline-parallel-size", fmt.Sprintf("%d", cfg.PipelineParallelSize),
		"--gpu-memory-utilization", fmt.Sprintf("%.2f", cfg.GPUMemoryUtilization),
		"--max-model-len", fmt.Sprintf("%d", cfg.MaxModelLen),
		"--dtype", cfg.DType,
	}

	if cfg.Quantization != "" {
		command = append(command, "--quantization", cfg.Quantization)
	}

	if cfg.MaxNumSeqs > 0 {
		command = append(command, "--max-num-seqs", fmt.Sprintf("%d", cfg.MaxNumSeqs))
	}

	if cfg.MaxNumBatchedTokens > 0 {
		command = append(command, "--max-num-batched-tokens", fmt.Sprintf("%d", cfg.MaxNumBatchedTokens))
	}

	// ExtraArgsJSON 暂时只存储，不自动拼接到命令。
	// 后面做 runtime adapter 时，再做严格白名单转换，避免用户注入危险参数。

	return command, shellJoin(command)
}

func shellJoin(args []string) string {
	escaped := make([]string, 0, len(args))

	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			escaped = append(escaped, fmt.Sprintf("%q", arg))
		} else {
			escaped = append(escaped, arg)
		}
	}

	return strings.Join(escaped, " ")
}

func normalizeStringDefault(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func isSupportedRuntimeType(runtimeType string) bool {
	switch runtimeType {
	case "vllm", "tensorrt_llm":
		return true
	default:
		return false
	}
}

func isSupportedAcceleratorType(acceleratorType string) bool {
	switch acceleratorType {
	case "nvidia", "hygon_dcu":
		return true
	default:
		return false
	}
}

func decorateDeployment(deployment *domain.Deployment) {
	deployment.RuntimeConfig.RequiredDeviceCount = calculateRequiredDeviceCount(deployment.RuntimeConfig)
}

func calculateRequiredDeviceCount(cfg domain.DeploymentRuntimeConfig) int {
	tp := cfg.TensorParallelSize
	pp := cfg.PipelineParallelSize

	if tp <= 0 {
		tp = 1
	}

	if pp <= 0 {
		pp = 1
	}

	return tp * pp
}
