package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultTaskCacheTTL = 24 * time.Hour

type TaskSnapshot struct {
	ID        uint      `json:"id"`
	TaskType  string    `json:"task_type"`
	Status    string    `json:"status"`
	Progress  int       `json:"progress"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TaskCache struct {
	rdb *redis.Client
}

func NewTaskCache(rdb *redis.Client) *TaskCache {
	return &TaskCache{rdb: rdb}
}

func (c *TaskCache) Set(ctx context.Context, snapshot TaskSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	key := taskStatusKey(snapshot.ID)
	return c.rdb.Set(ctx, key, data, defaultTaskCacheTTL).Err()
}

func (c *TaskCache) Get(ctx context.Context, taskID uint) (*TaskSnapshot, error) {
	key := taskStatusKey(taskID)

	data, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}

	var snapshot TaskSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}

	return &snapshot, nil
}

func taskStatusKey(taskID uint) string {
	return fmt.Sprintf("task:%d:status", taskID)
}
