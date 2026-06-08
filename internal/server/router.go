package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/WindAdherent/llm-platform/internal/api"
	"github.com/WindAdherent/llm-platform/internal/config"
)

func NewRouter(cfg config.Config, db *gorm.DB) *gin.Engine {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	r.GET("/healthz", func(c *gin.Context) {
		sqlDB, err := db.DB()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error",
				"app":    cfg.AppName,
				"env":    cfg.AppEnv,
				"mysql":  "error",
				"error":  err.Error(),
			})
			return
		}

		if err := sqlDB.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "error",
				"app":    cfg.AppName,
				"env":    cfg.AppEnv,
				"mysql":  "down",
				"error":  err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"app":    cfg.AppName,
			"env":    cfg.AppEnv,
			"mysql":  "ok",
		})
	})

	apiV1 := r.Group("/api/v1")
	{
		apiV1.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"message": "pong",
			})
		})

		modelHandler := api.NewModelHandler(db)

		models := apiV1.Group("/models")
		{
			models.POST("", modelHandler.CreateModel)
			models.GET("", modelHandler.ListModels)
			models.GET("/:id", modelHandler.GetModel)
		}
	}

	return r
}
