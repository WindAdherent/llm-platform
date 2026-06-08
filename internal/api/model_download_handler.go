package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	platformcache "github.com/WindAdherent/llm-platform/internal/cache"
	"github.com/WindAdherent/llm-platform/internal/domain"
)

type ModelDownloadHandler struct {
	db        *gorm.DB
	taskCache *platformcache.TaskCache
}

func NewModelDownloadHandler(db *gorm.DB, rdb *redis.Client) *ModelDownloadHandler {
	return &ModelDownloadHandler{
		db:        db,
		taskCache: platformcache.NewTaskCache(rdb),
	}
}

type StartModelDownloadRequest struct {
	LocalPath string `json:"local_path"`
	Force     bool   `json:"force"`
}

func (h *ModelDownloadHandler) StartModelVersionDownload(c *gin.Context) {
	modelID, err := parseUintParam(c, "model_id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid model id",
		})
		return
	}

	versionID, err := parseUintParam(c, "version_id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid version id",
		})
		return
	}

	var req StartModelDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request body",
			"details": err.Error(),
		})
		return
	}

	var model domain.Model
	if err := h.db.First(&model, modelID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "model not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get model",
			"details": err.Error(),
		})
		return
	}

	var version domain.ModelVersion
	if err := h.db.
		Where("id = ? AND model_id = ?", versionID, modelID).
		First(&version).Error; err != nil {
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

	if !req.Force {
		switch version.Status {
		case "DOWNLOADING":
			c.JSON(http.StatusConflict, gin.H{
				"error": "model version is already downloading",
			})
			return
		case "READY":
			c.JSON(http.StatusConflict, gin.H{
				"error": "model version is already ready, set force=true to create a new download task",
			})
			return
		}
	}

	localPath := strings.TrimSpace(req.LocalPath)
	if localPath == "" {
		localPath = defaultModelLocalPath(model, version)
	}

	task := domain.Task{
		TaskType: "MODEL_DOWNLOAD",
		Status:   "PENDING",
		Progress: 0,
		Message: fmt.Sprintf(
			"prepare to download model %s version %s from %s",
			model.Name,
			version.VersionName,
			model.SourceURI,
		),
	}

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}

		return tx.Model(&version).Updates(map[string]interface{}{
			"status":     "DOWNLOADING",
			"local_path": localPath,
		}).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create model download task",
			"details": err.Error(),
		})
		return
	}

	if err := h.db.First(&version, version.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to reload model version",
			"details": err.Error(),
		})
		return
	}

	snapshot := platformcache.TaskSnapshot{
		ID:        task.ID,
		TaskType:  task.TaskType,
		Status:    task.Status,
		Progress:  task.Progress,
		Message:   task.Message,
		UpdatedAt: task.UpdatedAt,
	}

	if err := h.taskCache.Set(c.Request.Context(), snapshot); err != nil {
		c.JSON(http.StatusAccepted, gin.H{
			"task":          task,
			"model":         model,
			"model_version": version,
			"cache_warning": err.Error(),
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"task":          task,
		"model":         model,
		"model_version": version,
	})
}

type CompleteModelDownloadRequest struct {
	TaskID       uint   `json:"task_id" binding:"required"`
	Success      bool   `json:"success"`
	LocalPath    string `json:"local_path"`
	ErrorMessage string `json:"error_message"`
}

func (h *ModelDownloadHandler) CompleteModelVersionDownload(c *gin.Context) {
	modelID, err := parseUintParam(c, "model_id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid model id",
		})
		return
	}

	versionID, err := parseUintParam(c, "version_id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid version id",
		})
		return
	}

	var req CompleteModelDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request body",
			"details": err.Error(),
		})
		return
	}

	var version domain.ModelVersion
	if err := h.db.
		Where("id = ? AND model_id = ?", versionID, modelID).
		First(&version).Error; err != nil {
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

	var task domain.Task
	if err := h.db.
		Where("id = ? AND task_type = ?", req.TaskID, "MODEL_DOWNLOAD").
		First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "download task not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get download task",
			"details": err.Error(),
		})
		return
	}

	now := time.Now()

	versionStatus := "READY"
	taskStatus := "SUCCEEDED"
	progress := 100
	message := "model download completed"
	errorMessage := ""

	localPath := strings.TrimSpace(req.LocalPath)
	if localPath == "" {
		localPath = version.LocalPath
	}

	if !req.Success {
		versionStatus = "FAILED"
		taskStatus = "FAILED"
		progress = task.Progress
		message = "model download failed"
		errorMessage = req.ErrorMessage

		if errorMessage == "" {
			errorMessage = "unknown error"
		}
	}

	resultJSON := mapToJSONStringPtr(map[string]interface{}{
		"model_id":         modelID,
		"model_version_id": versionID,
		"local_path":       localPath,
		"success":          req.Success,
	})

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		versionUpdates := map[string]interface{}{
			"status": versionStatus,
		}

		if localPath != "" {
			versionUpdates["local_path"] = localPath
		}

		if err := tx.Model(&version).Updates(versionUpdates).Error; err != nil {
			return err
		}

		taskUpdates := map[string]interface{}{
			"status":        taskStatus,
			"progress":      progress,
			"message":       message,
			"finished_at":   &now,
			"result_json":   resultJSON,
			"error_message": errorMessage,
		}

		if task.StartedAt == nil {
			taskUpdates["started_at"] = &now
		}

		return tx.Model(&task).Updates(taskUpdates).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to complete model download",
			"details": err.Error(),
		})
		return
	}

	if err := h.db.First(&version, version.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to reload model version",
			"details": err.Error(),
		})
		return
	}

	if err := h.db.First(&task, task.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to reload task",
			"details": err.Error(),
		})
		return
	}

	snapshot := platformcache.TaskSnapshot{
		ID:        task.ID,
		TaskType:  task.TaskType,
		Status:    task.Status,
		Progress:  task.Progress,
		Message:   task.Message,
		Error:     task.ErrorMessage,
		UpdatedAt: task.UpdatedAt,
	}

	if err := h.taskCache.Set(context.Background(), snapshot); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"task":          task,
			"model_version": version,
			"cache_warning": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"task":          task,
		"model_version": version,
	})
}

func defaultModelLocalPath(model domain.Model, version domain.ModelVersion) string {
	family := sanitizeModelPathPart(model.Family)
	name := sanitizeModelPathPart(model.Name)
	versionName := sanitizeModelPathPart(version.VersionName)

	return fmt.Sprintf("/data/models/%s/%s/%s", family, name, versionName)
}

func sanitizeModelPathPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, "\\", "-")

	if value == "" {
		return "unknown"
	}

	return value
}

func mapToJSONStringPtr(value map[string]interface{}) *string {
	data, err := json.Marshal(value)
	if err != nil {
		fallback := "{}"
		return &fallback
	}

	result := string(data)
	return &result
}
