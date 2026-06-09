package worker

import "github.com/WindAdherent/llm-platform/internal/domain"

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
