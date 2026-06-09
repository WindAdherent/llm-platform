package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultRuntimeEndpointCacheTTL = 24 * time.Hour

type RuntimeEndpointSnapshot struct {
	DeploymentID uint      `json:"deployment_id"`
	RuntimeType  string    `json:"runtime_type"`
	ExposureType string    `json:"exposure_type"`
	InternalURL  string    `json:"internal_url"`
	ExternalURL  string    `json:"external_url"`
	Status       string    `json:"status"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RuntimeEndpointCache struct {
	rdb *redis.Client
}

func NewRuntimeEndpointCache(rdb *redis.Client) *RuntimeEndpointCache {
	return &RuntimeEndpointCache{rdb: rdb}
}

func (c *RuntimeEndpointCache) Set(ctx context.Context, snapshot RuntimeEndpointSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	return c.rdb.Set(ctx, runtimeEndpointKey(snapshot.DeploymentID), data, defaultRuntimeEndpointCacheTTL).Err()
}

func (c *RuntimeEndpointCache) Get(ctx context.Context, deploymentID uint) (*RuntimeEndpointSnapshot, error) {
	data, err := c.rdb.Get(ctx, runtimeEndpointKey(deploymentID)).Bytes()
	if err != nil {
		return nil, err
	}

	var snapshot RuntimeEndpointSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}

	return &snapshot, nil
}

func (c *RuntimeEndpointCache) Delete(ctx context.Context, deploymentID uint) error {
	return c.rdb.Del(ctx, runtimeEndpointKey(deploymentID)).Err()
}

func runtimeEndpointKey(deploymentID uint) string {
	return fmt.Sprintf("runtime:endpoint:deployment:%d", deploymentID)
}
