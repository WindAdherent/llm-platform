package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	platformcache "github.com/WindAdherent/llm-platform/internal/cache"
	"github.com/WindAdherent/llm-platform/internal/config"
	"github.com/WindAdherent/llm-platform/internal/domain"
)

type ModelDownloadWorker struct {
	db           *gorm.DB
	taskCache    *platformcache.TaskCache
	cfg          config.Config
	pollInterval time.Duration
}

type ModelDownloadPayload struct {
	ModelID        uint   `json:"model_id"`
	ModelVersionID uint   `json:"model_version_id"`
	ModelName      string `json:"model_name"`
	VersionName    string `json:"version_name"`
	SourceType     string `json:"source_type"`
	SourceURI      string `json:"source_uri"`
	Revision       string `json:"revision"`
	LocalPath      string `json:"local_path"`
}

func NewModelDownloadWorker(db *gorm.DB, taskCache *platformcache.TaskCache, cfg config.Config) *ModelDownloadWorker {
	return &ModelDownloadWorker{
		db:           db,
		taskCache:    taskCache,
		cfg:          cfg,
		pollInterval: 10 * time.Second,
	}
}

func (w *ModelDownloadWorker) Run(ctx context.Context) error {
	log.Println("model download worker started")

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		if err := w.processOnce(ctx); err != nil {
			log.Printf("model download worker error: %v", err)
		}

		select {
		case <-ctx.Done():
			log.Println("model download worker stopped")
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *ModelDownloadWorker) processOnce(ctx context.Context) error {
	task, found, err := w.claimPendingTask(ctx)
	if err != nil {
		return err
	}

	if !found {
		return nil
	}

	log.Printf("claimed MODEL_DOWNLOAD task id=%d", task.ID)

	if err := w.cacheTask(ctx, task); err != nil {
		log.Printf("failed to cache task snapshot, task_id=%d, err=%v", task.ID, err)
	}

	payload, err := parseModelDownloadPayload(task)
	if err != nil {
		return w.failTask(ctx, task.ID, 0, "invalid model download payload", err)
	}

	switch w.cfg.ModelDownloadMode {
	case "simulated", "":
		return w.simulateDownload(ctx, task.ID, payload)
	case "local":
		return w.runLocalDownloader(ctx, task.ID, payload)
	default:
		return w.failTask(ctx, task.ID, payload.ModelVersionID, "unsupported model download mode", fmt.Errorf("unsupported MODEL_DOWNLOAD_MODE=%s", w.cfg.ModelDownloadMode))
	}
}

func (w *ModelDownloadWorker) claimPendingTask(ctx context.Context) (domain.Task, bool, error) {
	var claimed domain.Task

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task domain.Task

		err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_type = ? AND status = ?", "MODEL_DOWNLOAD", "PENDING").
			Order("id ASC").
			First(&task).Error

		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		now := time.Now()

		updates := map[string]interface{}{
			"status":     "RUNNING",
			"progress":   1,
			"message":    "model download task started",
			"started_at": &now,
		}

		if err := tx.Model(&task).Updates(updates).Error; err != nil {
			return err
		}

		if err := tx.First(&claimed, task.ID).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return domain.Task{}, false, err
	}

	if claimed.ID == 0 {
		return domain.Task{}, false, nil
	}

	return claimed, true, nil
}

func parseModelDownloadPayload(task domain.Task) (ModelDownloadPayload, error) {
	if task.PayloadJSON == nil || *task.PayloadJSON == "" {
		return ModelDownloadPayload{}, fmt.Errorf("task payload_json is empty")
	}

	var payload ModelDownloadPayload
	if err := json.Unmarshal([]byte(*task.PayloadJSON), &payload); err != nil {
		return ModelDownloadPayload{}, err
	}

	if payload.ModelID == 0 {
		return ModelDownloadPayload{}, fmt.Errorf("model_id is required")
	}

	if payload.ModelVersionID == 0 {
		return ModelDownloadPayload{}, fmt.Errorf("model_version_id is required")
	}

	if payload.LocalPath == "" {
		return ModelDownloadPayload{}, fmt.Errorf("local_path is required")
	}

	return payload, nil
}

func (w *ModelDownloadWorker) simulateDownload(ctx context.Context, taskID uint, payload ModelDownloadPayload) error {
	steps := []struct {
		progress int
		message  string
	}{
		{10, "checking model source"},
		{30, "resolving model files"},
		{50, "downloading model files"},
		{80, "verifying model files"},
		{95, "finalizing model cache"},
	}

	for _, step := range steps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}

		task, err := w.updateTaskProgress(ctx, taskID, step.progress, step.message)
		if err != nil {
			return err
		}

		if err := w.cacheTask(ctx, task); err != nil {
			log.Printf("failed to cache task snapshot, task_id=%d, err=%v", task.ID, err)
		}
	}

	return w.completeTask(ctx, taskID, payload)
}

type DownloaderEvent struct {
	Type           string `json:"type"`
	Progress       int    `json:"progress"`
	Message        string `json:"message"`
	Error          string `json:"error"`
	LocalPath      string `json:"local_path"`
	ResultPath     string `json:"result_path"`
	DownloadedPath string `json:"downloaded_path"`
}

func (w *ModelDownloadWorker) runLocalDownloader(ctx context.Context, taskID uint, payload ModelDownloadPayload) error {
	args := strings.Fields(w.cfg.ModelDownloaderCommand)
	if len(args) == 0 {
		return w.failTask(ctx, taskID, payload.ModelVersionID, "model downloader command is empty", fmt.Errorf("MODEL_DOWNLOADER_COMMAND is empty"))
	}

	args = append(args,
		"--source-type", payload.SourceType,
		"--source-uri", payload.SourceURI,
		"--revision", payload.Revision,
		"--local-path", payload.LocalPath,
	)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return w.failTask(ctx, taskID, payload.ModelVersionID, "failed to create downloader stdout pipe", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return w.failTask(ctx, taskID, payload.ModelVersionID, "failed to create downloader stderr pipe", err)
	}

	if err := cmd.Start(); err != nil {
		return w.failTask(ctx, taskID, payload.ModelVersionID, "failed to start model downloader", err)
	}

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)

		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("model downloader stderr, task_id=%d: %s", taskID, scanner.Text())
		}
	}()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event DownloaderEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			log.Printf("model downloader non-json output, task_id=%d: %s", taskID, line)
			continue
		}

		if err := w.handleDownloaderEvent(ctx, taskID, payload, event); err != nil {
			log.Printf("failed to handle downloader event, task_id=%d, err=%v", taskID, err)
		}
	}

	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		return w.failTask(ctx, taskID, payload.ModelVersionID, "failed to read downloader output", err)
	}

	err = cmd.Wait()
	<-stderrDone

	if err != nil {
		return w.failTask(ctx, taskID, payload.ModelVersionID, "model downloader command failed", err)
	}

	return w.completeTask(ctx, taskID, payload)
}

func (w *ModelDownloadWorker) handleDownloaderEvent(ctx context.Context, taskID uint, payload ModelDownloadPayload, event DownloaderEvent) error {
	message := event.Message
	if message == "" {
		message = fmt.Sprintf("downloader event: %s", event.Type)
	}

	progress := event.Progress
	if progress < 0 {
		progress = 0
	}
	if progress > 99 {
		progress = 99
	}

	switch event.Type {
	case "started", "progress":
		task, err := w.updateTaskProgress(ctx, taskID, progress, message)
		if err != nil {
			return err
		}

		return w.cacheTask(ctx, task)

	case "failed":
		cause := fmt.Errorf(event.Error)
		if event.Error == "" {
			cause = fmt.Errorf("model downloader reported failure")
		}

		return w.failTask(ctx, taskID, payload.ModelVersionID, message, cause)

	case "completed":
		task, err := w.updateTaskProgress(ctx, taskID, 99, message)
		if err != nil {
			return err
		}

		return w.cacheTask(ctx, task)

	default:
		log.Printf("unknown downloader event type, task_id=%d, type=%s", taskID, event.Type)
		return nil
	}
}

func (w *ModelDownloadWorker) updateTaskProgress(ctx context.Context, taskID uint, progress int, message string) (domain.Task, error) {
	var task domain.Task

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&task, taskID).Error; err != nil {
			return err
		}

		if task.Status != "RUNNING" {
			return fmt.Errorf("task %d is not running, current status=%s", task.ID, task.Status)
		}

		updates := map[string]interface{}{
			"progress": progress,
			"message":  message,
		}

		if err := tx.Model(&task).Updates(updates).Error; err != nil {
			return err
		}

		return tx.First(&task, taskID).Error
	})

	return task, err
}

func (w *ModelDownloadWorker) completeTask(ctx context.Context, taskID uint, payload ModelDownloadPayload) error {
	now := time.Now()

	resultJSON := mapToJSONStringPtr(map[string]interface{}{
		"model_id":         payload.ModelID,
		"model_version_id": payload.ModelVersionID,
		"model_name":       payload.ModelName,
		"version_name":     payload.VersionName,
		"local_path":       payload.LocalPath,
		"source_type":      payload.SourceType,
		"source_uri":       payload.SourceURI,
		"simulated":        true,
	})

	var task domain.Task

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var version domain.ModelVersion
		if err := tx.
			Where("id = ? AND model_id = ?", payload.ModelVersionID, payload.ModelID).
			First(&version).Error; err != nil {
			return err
		}

		if err := tx.Model(&version).Updates(map[string]interface{}{
			"status":     "READY",
			"local_path": payload.LocalPath,
		}).Error; err != nil {
			return err
		}

		if err := tx.First(&task, taskID).Error; err != nil {
			return err
		}

		if err := tx.Model(&task).Updates(map[string]interface{}{
			"status":      "SUCCEEDED",
			"progress":    100,
			"message":     "model download completed",
			"finished_at": &now,
			"result_json": resultJSON,
		}).Error; err != nil {
			return err
		}

		return tx.First(&task, taskID).Error
	})

	if err != nil {
		return w.failTask(ctx, taskID, payload.ModelVersionID, "model download failed", err)
	}

	if err := w.cacheTask(ctx, task); err != nil {
		log.Printf("failed to cache task snapshot, task_id=%d, err=%v", task.ID, err)
	}

	log.Printf("MODEL_DOWNLOAD task completed, task_id=%d, model_version_id=%d", taskID, payload.ModelVersionID)

	return nil
}

func (w *ModelDownloadWorker) failTask(ctx context.Context, taskID uint, modelVersionID uint, message string, cause error) error {
	now := time.Now()
	errorMessage := cause.Error()

	var task domain.Task

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if modelVersionID != 0 {
			var version domain.ModelVersion
			if err := tx.First(&version, modelVersionID).Error; err == nil {
				if err := tx.Model(&version).Updates(map[string]interface{}{
					"status": "FAILED",
				}).Error; err != nil {
					return err
				}
			}
		}

		if err := tx.First(&task, taskID).Error; err != nil {
			return err
		}

		if err := tx.Model(&task).Updates(map[string]interface{}{
			"status":        "FAILED",
			"message":       message,
			"error_message": errorMessage,
			"finished_at":   &now,
		}).Error; err != nil {
			return err
		}

		return tx.First(&task, taskID).Error
	})

	if err != nil {
		return err
	}

	if err := w.cacheTask(ctx, task); err != nil {
		log.Printf("failed to cache failed task snapshot, task_id=%d, err=%v", task.ID, err)
	}

	return cause
}

func (w *ModelDownloadWorker) cacheTask(ctx context.Context, task domain.Task) error {
	return w.taskCache.Set(ctx, platformcache.TaskSnapshot{
		ID:        task.ID,
		TaskType:  task.TaskType,
		Status:    task.Status,
		Progress:  task.Progress,
		Message:   task.Message,
		Error:     task.ErrorMessage,
		UpdatedAt: task.UpdatedAt,
	})
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
