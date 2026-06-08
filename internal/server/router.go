package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/WindAdherent/llm-platform/internal/api"
	"github.com/WindAdherent/llm-platform/internal/config"
	"github.com/WindAdherent/llm-platform/internal/storage"
)

func NewRouter(cfg config.Config, db *gorm.DB, rdb *redis.Client, objectStorage *storage.ObjectStorage) *gin.Engine {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	r.GET("/healthz", func(c *gin.Context) {
		sqlDB, err := db.DB()
		mysqlStatus := "ok"
		redisStatus := "ok"
		minioStatus := "ok"

		if err != nil {
			mysqlStatus = "error"
		} else if err := sqlDB.Ping(); err != nil {
			mysqlStatus = "down"
		}

		if err := rdb.Ping(c.Request.Context()).Err(); err != nil {
			redisStatus = "down"
		}

		if err := objectStorage.Health(c.Request.Context()); err != nil {
			minioStatus = "down"
		}

		status := "ok"
		httpStatus := http.StatusOK

		if mysqlStatus != "ok" || redisStatus != "ok" || minioStatus != "ok" {
			status = "error"
			httpStatus = http.StatusServiceUnavailable
		}

		c.JSON(httpStatus, gin.H{
			"status": status,
			"app":    cfg.AppName,
			"env":    cfg.AppEnv,
			"mysql":  mysqlStatus,
			"redis":  redisStatus,
			"minio":  minioStatus,
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
		modelDownloadHandler := api.NewModelDownloadHandler(db, rdb)
		models := apiV1.Group("/models")
		{
			models.POST("", modelHandler.CreateModel)
			models.GET("", modelHandler.ListModels)
			models.GET("/:id", modelHandler.GetModel)

			models.POST("/:model_id/versions/:version_id/download", modelDownloadHandler.StartModelVersionDownload)
			models.POST("/:model_id/versions/:version_id/download/complete", modelDownloadHandler.CompleteModelVersionDownload)
		}

		taskHandler := api.NewTaskHandler(db, rdb)
		tasks := apiV1.Group("/tasks")
		{
			tasks.POST("", taskHandler.CreateTask)
			tasks.GET("", taskHandler.ListTasks)
			tasks.GET("/:id", taskHandler.GetTask)
			tasks.PATCH("/:id", taskHandler.UpdateTask)
		}

		objectHandler := api.NewObjectHandler(objectStorage)
		objects := apiV1.Group("/objects")
		{
			objects.POST("/upload", objectHandler.UploadObject)
			objects.GET("", objectHandler.ListObjects)
			objects.GET("/presigned-url", objectHandler.PresignedGetObjectURL)
		}
	}

	return r
}
