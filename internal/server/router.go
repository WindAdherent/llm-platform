package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/WindAdherent/llm-platform/internal/api"
	"github.com/WindAdherent/llm-platform/internal/config"
)

func NewRouter(cfg config.Config, db *gorm.DB, rdb *redis.Client) *gin.Engine {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	r.GET("/healthz", func(c *gin.Context) {
		sqlDB, err := db.DB()
		mysqlStatus := "ok"
		redisStatus := "ok"

		if err != nil {
			mysqlStatus = "error"
		} else if err := sqlDB.Ping(); err != nil {
			mysqlStatus = "down"
		}

		if err := rdb.Ping(c.Request.Context()).Err(); err != nil {
			redisStatus = "down"
		}

		status := "ok"
		httpStatus := http.StatusOK

		if mysqlStatus != "ok" || redisStatus != "ok" {
			status = "error"
			httpStatus = http.StatusServiceUnavailable
		}

		c.JSON(httpStatus, gin.H{
			"status": status,
			"app":    cfg.AppName,
			"env":    cfg.AppEnv,
			"mysql":  mysqlStatus,
			"redis":  redisStatus,
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

		taskHandler := api.NewTaskHandler(db, rdb)
		tasks := apiV1.Group("/tasks")
		{
			tasks.POST("", taskHandler.CreateTask)
			tasks.GET("", taskHandler.ListTasks)
			tasks.GET("/:id", taskHandler.GetTask)
			tasks.PATCH("/:id", taskHandler.UpdateTask)
		}
	}

	return r
}
