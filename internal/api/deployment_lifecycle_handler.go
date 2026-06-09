package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	platformcache "github.com/WindAdherent/llm-platform/internal/cache"
	"github.com/WindAdherent/llm-platform/internal/domain"
)

type DeploymentLifecycleHandler struct {
	db        *gorm.DB
	taskCache *platformcache.TaskCache
}

func NewDeploymentLifecycleHandler(db *gorm.DB, rdb *redis.Client) *DeploymentLifecycleHandler {
	return &DeploymentLifecycleHandler{
		db:        db,
		taskCache: platformcache.NewTaskCache(rdb),
	}
}

type StartDeploymentRequest struct {
	Message string `json:"message"`
}

func (h *DeploymentLifecycleHandler) StartDeployment(c *gin.Context) {
	deploymentID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid deployment id",
		})
		return
	}

	var req StartDeploymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request body",
			"details": err.Error(),
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

	if deployment.Status == "RUNNING" || deployment.Status == "STARTING" {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "deployment is already active",
			"status": deployment.Status,
		})
		return
	}

	if deployment.ModelVersion.Status != "READY" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":                "model version is not ready",
			"model_version_status": deployment.ModelVersion.Status,
		})
		return
	}

	if deployment.RuntimeConfig.ExposureType == "docker_host_port" && deployment.RuntimeConfig.HostPort == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "host_port is required when exposure_type is docker_host_port",
		})
		return
	}

	payloadJSON := mapToJSONStringPtr(map[string]interface{}{
		"deployment_id":         deployment.ID,
		"deployment_name":       deployment.Name,
		"runtime_type":          deployment.RuntimeType,
		"accelerator_type":      deployment.AcceleratorType,
		"exposure_type":         deployment.RuntimeConfig.ExposureType,
		"required_device_count": calculateRequiredDeviceCount(deployment.RuntimeConfig),
	})

	message := req.Message
	if message == "" {
		message = fmt.Sprintf("prepare to start deployment %s", deployment.Name)
	}

	task := domain.Task{
		TaskType:    "DEPLOYMENT_START",
		Status:      "PENDING",
		Progress:    0,
		Message:     message,
		PayloadJSON: payloadJSON,
	}

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}

		return tx.Model(&deployment).Updates(map[string]interface{}{
			"status": "STARTING",
		}).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create deployment start task",
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

	if err := h.cacheTask(c.Request.Context(), task); err != nil {
		c.JSON(http.StatusAccepted, gin.H{
			"task":          task,
			"deployment":    deployment,
			"cache_warning": err.Error(),
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"task":       task,
		"deployment": deployment,
	})
}

type StopDeploymentRequest struct {
	Message string `json:"message"`
}

func (h *DeploymentLifecycleHandler) StopDeployment(c *gin.Context) {
	deploymentID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid deployment id",
		})
		return
	}

	var req StopDeploymentRequest
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

	if deployment.Status == "STOPPED" || deployment.Status == "STOPPING" || deployment.Status == "CREATED" {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "deployment is not running",
			"status": deployment.Status,
		})
		return
	}

	payloadJSON := mapToJSONStringPtr(map[string]interface{}{
		"deployment_id":   deployment.ID,
		"deployment_name": deployment.Name,
		"runtime_type":    deployment.RuntimeType,
		"exposure_type":   deployment.RuntimeConfig.ExposureType,
	})

	message := req.Message
	if message == "" {
		message = fmt.Sprintf("prepare to stop deployment %s", deployment.Name)
	}

	task := domain.Task{
		TaskType:    "DEPLOYMENT_STOP",
		Status:      "PENDING",
		Progress:    0,
		Message:     message,
		PayloadJSON: payloadJSON,
	}

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}

		return tx.Model(&deployment).Updates(map[string]interface{}{
			"status": "STOPPING",
		}).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create deployment stop task",
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

	if err := h.cacheTask(c.Request.Context(), task); err != nil {
		c.JSON(http.StatusAccepted, gin.H{
			"task":          task,
			"deployment":    deployment,
			"cache_warning": err.Error(),
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"task":       task,
		"deployment": deployment,
	})
}

func (h *DeploymentLifecycleHandler) cacheTask(ctx context.Context, task domain.Task) error {
	return h.taskCache.Set(ctx, platformcache.TaskSnapshot{
		ID:        task.ID,
		TaskType:  task.TaskType,
		Status:    task.Status,
		Progress:  task.Progress,
		Message:   task.Message,
		Error:     task.ErrorMessage,
		UpdatedAt: task.UpdatedAt,
	})
}

func nowPtr() *time.Time {
	now := time.Now()
	return &now
}
