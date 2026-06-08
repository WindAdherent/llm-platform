package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	platformcache "github.com/WindAdherent/llm-platform/internal/cache"
	"github.com/WindAdherent/llm-platform/internal/domain"
)

type TaskHandler struct {
	db        *gorm.DB
	taskCache *platformcache.TaskCache
}

func NewTaskHandler(db *gorm.DB, rdb *redis.Client) *TaskHandler {
	return &TaskHandler{
		db:        db,
		taskCache: platformcache.NewTaskCache(rdb),
	}
}

type CreateTaskRequest struct {
	TaskType string `json:"task_type" binding:"required"`
	Message  string `json:"message"`
}

func (h *TaskHandler) CreateTask(c *gin.Context) {
	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request body",
			"details": err.Error(),
		})
		return
	}

	task := domain.Task{
		TaskType: req.TaskType,
		Status:   "PENDING",
		Progress: 0,
		Message:  req.Message,
	}

	if err := h.db.Create(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create task",
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
		c.JSON(http.StatusCreated, gin.H{
			"task":          task,
			"cache_warning": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, task)
}

func (h *TaskHandler) ListTasks(c *gin.Context) {
	var tasks []domain.Task

	if err := h.db.
		Order("id desc").
		Limit(100).
		Find(&tasks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to list tasks",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items": tasks,
		"total": len(tasks),
	})
}

func (h *TaskHandler) GetTask(c *gin.Context) {
	taskID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid task id",
		})
		return
	}

	cacheSnapshot, cacheErr := h.taskCache.Get(c.Request.Context(), taskID)

	var task domain.Task
	dbErr := h.db.First(&task, taskID).Error
	if dbErr != nil {
		if dbErr == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "task not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get task",
			"details": dbErr.Error(),
		})
		return
	}

	resp := gin.H{
		"task": task,
	}

	if cacheErr == nil {
		resp["cache"] = cacheSnapshot
	} else if cacheErr != redis.Nil {
		resp["cache_warning"] = cacheErr.Error()
	}

	c.JSON(http.StatusOK, resp)
}

type UpdateTaskRequest struct {
	Status       string           `json:"status"`
	Progress     *int             `json:"progress"`
	Message      string           `json:"message"`
	ErrorMessage string           `json:"error_message"`
	ResultJSON   *json.RawMessage `json:"result_json"`
}

func (h *TaskHandler) UpdateTask(c *gin.Context) {
	taskID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid task id",
		})
		return
	}

	var req UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request body",
			"details": err.Error(),
		})
		return
	}

	var task domain.Task
	if err := h.db.First(&task, taskID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "task not found",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to get task",
			"details": err.Error(),
		})
		return
	}

	now := time.Now()
	updates := map[string]interface{}{}

	if req.Status != "" {
		if !isValidTaskStatus(req.Status) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid task status",
				"supported_statuses": []string{
					"PENDING",
					"RUNNING",
					"SUCCEEDED",
					"FAILED",
					"CANCELED",
				},
			})
			return
		}

		updates["status"] = req.Status

		if req.Status == "RUNNING" && task.StartedAt == nil {
			updates["started_at"] = &now
		}

		if isTerminalTaskStatus(req.Status) && task.FinishedAt == nil {
			updates["finished_at"] = &now
		}
	}

	if req.Progress != nil {
		if *req.Progress < 0 || *req.Progress > 100 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "progress must be between 0 and 100",
			})
			return
		}
		updates["progress"] = *req.Progress
	}

	if req.Message != "" {
		updates["message"] = req.Message
	}

	if req.ErrorMessage != "" {
		updates["error_message"] = req.ErrorMessage
	}

	if req.ResultJSON != nil {
		raw := strings.TrimSpace(string(*req.ResultJSON))

		if raw == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "result_json cannot be empty",
			})
			return
		}

		if raw == "null" {
			updates["result_json"] = nil
		} else {
			if !json.Valid([]byte(raw)) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "result_json must be valid JSON",
				})
				return
			}

			updates["result_json"] = raw
		}
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "no fields to update",
		})
		return
	}

	if err := h.db.Model(&task).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to update task",
			"details": err.Error(),
		})
		return
	}

	if err := h.db.First(&task, taskID).Error; err != nil {
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
			"cache_warning": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, task)
}

func parseUintParam(c *gin.Context, name string) (uint, error) {
	raw := c.Param(name)
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(value), nil
}

func isValidTaskStatus(status string) bool {
	switch status {
	case "PENDING", "RUNNING", "SUCCEEDED", "FAILED", "CANCELED":
		return true
	default:
		return false
	}
}

func isTerminalTaskStatus(status string) bool {
	switch status {
	case "SUCCEEDED", "FAILED", "CANCELED":
		return true
	default:
		return false
	}
}
