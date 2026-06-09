package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	platformcache "github.com/WindAdherent/llm-platform/internal/cache"
	"github.com/WindAdherent/llm-platform/internal/config"
	"github.com/WindAdherent/llm-platform/internal/domain"
)

type DeploymentWorker struct {
	db            *gorm.DB
	taskCache     *platformcache.TaskCache
	endpointCache *platformcache.RuntimeEndpointCache
	cfg           config.Config
	pollInterval  time.Duration
}

type DeploymentTaskPayload struct {
	DeploymentID        uint   `json:"deployment_id"`
	DeploymentName      string `json:"deployment_name"`
	RuntimeType         string `json:"runtime_type"`
	AcceleratorType     string `json:"accelerator_type"`
	ExposureType        string `json:"exposure_type"`
	RequiredDeviceCount int    `json:"required_device_count"`
}

func NewDeploymentWorker(
	db *gorm.DB,
	taskCache *platformcache.TaskCache,
	endpointCache *platformcache.RuntimeEndpointCache,
	cfg config.Config,
) *DeploymentWorker {
	return &DeploymentWorker{
		db:            db,
		taskCache:     taskCache,
		endpointCache: endpointCache,
		cfg:           cfg,
		pollInterval:  3 * time.Second,
	}
}

func (w *DeploymentWorker) Run(ctx context.Context) error {
	log.Println("deployment worker started")

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		if err := w.processOnce(ctx); err != nil {
			log.Printf("deployment worker error: %v", err)
		}

		select {
		case <-ctx.Done():
			log.Println("deployment worker stopped")
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *DeploymentWorker) processOnce(ctx context.Context) error {
	task, found, err := w.claimPendingTask(ctx)
	if err != nil {
		return err
	}

	if !found {
		return nil
	}

	log.Printf("claimed %s task id=%d", task.TaskType, task.ID)

	if err := w.cacheTask(ctx, task); err != nil {
		log.Printf("failed to cache deployment task, task_id=%d, err=%v", task.ID, err)
	}

	payload, err := parseDeploymentTaskPayload(task)
	if err != nil {
		return w.failTask(ctx, task.ID, 0, "invalid deployment task payload", err)
	}

	switch task.TaskType {
	case "DEPLOYMENT_START":
		return w.simulateStart(ctx, task.ID, payload)
	case "DEPLOYMENT_STOP":
		return w.simulateStop(ctx, task.ID, payload)
	default:
		return w.failTask(ctx, task.ID, payload.DeploymentID, "unsupported deployment task type", fmt.Errorf("unsupported task type %s", task.TaskType))
	}
}

func (w *DeploymentWorker) claimPendingTask(ctx context.Context) (domain.Task, bool, error) {
	var claimed domain.Task

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task domain.Task

		err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("task_type IN ? AND status = ?", []string{"DEPLOYMENT_START", "DEPLOYMENT_STOP"}, "PENDING").
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
			"message":    fmt.Sprintf("%s task started", strings.ToLower(task.TaskType)),
			"started_at": &now,
		}

		if err := tx.Model(&task).Updates(updates).Error; err != nil {
			return err
		}

		return tx.First(&claimed, task.ID).Error
	})

	if err != nil {
		return domain.Task{}, false, err
	}

	if claimed.ID == 0 {
		return domain.Task{}, false, nil
	}

	return claimed, true, nil
}

func parseDeploymentTaskPayload(task domain.Task) (DeploymentTaskPayload, error) {
	if task.PayloadJSON == nil || *task.PayloadJSON == "" {
		return DeploymentTaskPayload{}, fmt.Errorf("task payload_json is empty")
	}

	var payload DeploymentTaskPayload
	if err := json.Unmarshal([]byte(*task.PayloadJSON), &payload); err != nil {
		return DeploymentTaskPayload{}, err
	}

	if payload.DeploymentID == 0 {
		return DeploymentTaskPayload{}, fmt.Errorf("deployment_id is required")
	}

	return payload, nil
}

func (w *DeploymentWorker) simulateStart(ctx context.Context, taskID uint, payload DeploymentTaskPayload) error {
	steps := []struct {
		progress int
		message  string
	}{
		{10, "checking deployment config"},
		{30, "checking accelerator resources"},
		{50, "preparing runtime command"},
		{75, "starting runtime instance"},
		{90, "waiting for runtime health check"},
	}

	for _, step := range steps {
		if err := waitOrCancel(ctx, time.Second); err != nil {
			return err
		}

		task, err := w.updateTaskProgress(ctx, taskID, step.progress, step.message)
		if err != nil {
			return err
		}

		if err := w.cacheTask(ctx, task); err != nil {
			log.Printf("failed to cache deployment start progress, task_id=%d, err=%v", task.ID, err)
		}
	}

	return w.completeStart(ctx, taskID, payload)
}

func (w *DeploymentWorker) simulateStop(ctx context.Context, taskID uint, payload DeploymentTaskPayload) error {
	steps := []struct {
		progress int
		message  string
	}{
		{20, "sending stop signal to runtime"},
		{50, "waiting for runtime shutdown"},
		{80, "releasing runtime endpoint"},
	}

	for _, step := range steps {
		if err := waitOrCancel(ctx, time.Second); err != nil {
			return err
		}

		task, err := w.updateTaskProgress(ctx, taskID, step.progress, step.message)
		if err != nil {
			return err
		}

		if err := w.cacheTask(ctx, task); err != nil {
			log.Printf("failed to cache deployment stop progress, task_id=%d, err=%v", task.ID, err)
		}
	}

	return w.completeStop(ctx, taskID, payload)
}

func (w *DeploymentWorker) completeStart(ctx context.Context, taskID uint, payload DeploymentTaskPayload) error {
	now := time.Now()

	var task domain.Task
	var deployment domain.Deployment

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Preload("RuntimeConfig").
			First(&deployment, payload.DeploymentID).Error; err != nil {
			return err
		}

		endpoint := w.buildEndpoint(deployment)

		if endpoint == "" {
			return fmt.Errorf("failed to build endpoint for deployment %d", deployment.ID)
		}

		if err := tx.Model(&deployment).Updates(map[string]interface{}{
			"status":   "RUNNING",
			"endpoint": endpoint,
		}).Error; err != nil {
			return err
		}

		resultJSON := mapToJSONStringPtr(map[string]interface{}{
			"deployment_id":         deployment.ID,
			"endpoint":              endpoint,
			"exposure_type":         deployment.RuntimeConfig.ExposureType,
			"required_device_count": calculateRequiredDeviceCount(deployment.RuntimeConfig),
			"simulated":             true,
		})

		if err := tx.First(&task, taskID).Error; err != nil {
			return err
		}

		if err := tx.Model(&task).Updates(map[string]interface{}{
			"status":      "SUCCEEDED",
			"progress":    100,
			"message":     "deployment start completed",
			"finished_at": &now,
			"result_json": resultJSON,
		}).Error; err != nil {
			return err
		}

		return tx.First(&task, taskID).Error
	})

	if err != nil {
		return w.failTask(ctx, taskID, payload.DeploymentID, "deployment start failed", err)
	}

	if err := w.cacheTask(ctx, task); err != nil {
		log.Printf("failed to cache completed start task, task_id=%d, err=%v", task.ID, err)
	}

	snapshot := platformcache.RuntimeEndpointSnapshot{
		DeploymentID: deployment.ID,
		RuntimeType:  deployment.RuntimeType,
		ExposureType: deployment.RuntimeConfig.ExposureType,
		Status:       "RUNNING",
		UpdatedAt:    time.Now(),
	}

	switch deployment.RuntimeConfig.ExposureType {
	case "docker_host_port", "k8s_ingress":
		snapshot.ExternalURL = deployment.Endpoint
		snapshot.InternalURL = deployment.Endpoint
	default:
		snapshot.InternalURL = deployment.Endpoint
	}

	if err := w.endpointCache.Set(ctx, snapshot); err != nil {
		log.Printf("failed to cache runtime endpoint, deployment_id=%d, err=%v", deployment.ID, err)
	}

	log.Printf("DEPLOYMENT_START completed, task_id=%d, deployment_id=%d, endpoint=%s", taskID, deployment.ID, deployment.Endpoint)

	return nil
}

func (w *DeploymentWorker) completeStop(ctx context.Context, taskID uint, payload DeploymentTaskPayload) error {
	now := time.Now()

	var task domain.Task
	var deployment domain.Deployment

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&deployment, payload.DeploymentID).Error; err != nil {
			return err
		}

		if err := tx.Model(&deployment).Updates(map[string]interface{}{
			"status":   "STOPPED",
			"endpoint": "",
		}).Error; err != nil {
			return err
		}

		resultJSON := mapToJSONStringPtr(map[string]interface{}{
			"deployment_id": deployment.ID,
			"stopped":       true,
			"simulated":     true,
		})

		if err := tx.First(&task, taskID).Error; err != nil {
			return err
		}

		if err := tx.Model(&task).Updates(map[string]interface{}{
			"status":      "SUCCEEDED",
			"progress":    100,
			"message":     "deployment stop completed",
			"finished_at": &now,
			"result_json": resultJSON,
		}).Error; err != nil {
			return err
		}

		return tx.First(&task, taskID).Error
	})

	if err != nil {
		return w.failTask(ctx, taskID, payload.DeploymentID, "deployment stop failed", err)
	}

	if err := w.cacheTask(ctx, task); err != nil {
		log.Printf("failed to cache completed stop task, task_id=%d, err=%v", task.ID, err)
	}

	if err := w.endpointCache.Delete(ctx, payload.DeploymentID); err != nil {
		log.Printf("failed to delete runtime endpoint cache, deployment_id=%d, err=%v", payload.DeploymentID, err)
	}

	log.Printf("DEPLOYMENT_STOP completed, task_id=%d, deployment_id=%d", taskID, payload.DeploymentID)

	return nil
}

func (w *DeploymentWorker) updateTaskProgress(ctx context.Context, taskID uint, progress int, message string) (domain.Task, error) {
	var task domain.Task

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&task, taskID).Error; err != nil {
			return err
		}

		if task.Status != "RUNNING" {
			return fmt.Errorf("task %d is not running, current status=%s", task.ID, task.Status)
		}

		if err := tx.Model(&task).Updates(map[string]interface{}{
			"progress": progress,
			"message":  message,
		}).Error; err != nil {
			return err
		}

		return tx.First(&task, taskID).Error
	})

	return task, err
}

func (w *DeploymentWorker) failTask(ctx context.Context, taskID uint, deploymentID uint, message string, cause error) error {
	now := time.Now()
	errorMessage := cause.Error()

	var task domain.Task

	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if deploymentID != 0 {
			var deployment domain.Deployment
			if err := tx.First(&deployment, deploymentID).Error; err == nil {
				if err := tx.Model(&deployment).Updates(map[string]interface{}{
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
		log.Printf("failed to cache failed deployment task, task_id=%d, err=%v", task.ID, err)
	}

	return cause
}

func (w *DeploymentWorker) cacheTask(ctx context.Context, task domain.Task) error {
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

func (w *DeploymentWorker) buildEndpoint(deployment domain.Deployment) string {
	cfg := deployment.RuntimeConfig

	switch cfg.ExposureType {
	case "docker_host_port":
		if cfg.HostPort == nil {
			return ""
		}
		return fmt.Sprintf("http://%s:%d", w.cfg.RuntimeHost, *cfg.HostPort)

	case "k8s_cluster_ip":
		serviceName := sanitizeDNSName(deployment.Name)
		namespace := w.cfg.RuntimeK8SNamespace
		if namespace == "" {
			namespace = "llm"
		}
		return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, namespace, cfg.ServicePort)

	case "k8s_ingress":
		return fmt.Sprintf("http://%s/deployments/%s", w.cfg.RuntimeHost, sanitizeDNSName(deployment.Name))

	case "internal_gateway":
		return fmt.Sprintf("internal://deployment/%d", deployment.ID)

	default:
		return ""
	}
}

func sanitizeDNSName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, ".", "-")

	var builder strings.Builder

	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			builder.WriteRune(r)
		}
	}

	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "deployment"
	}

	return result
}

func waitOrCancel(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
		return nil
	}
}
