package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/WindAdherent/llm-platform/internal/domain"
)

type ModelHandler struct {
	db *gorm.DB
}

func NewModelHandler(db *gorm.DB) *ModelHandler {
	return &ModelHandler{db: db}
}

type CreateModelRequest struct {
	Name          string `json:"name" binding:"required"`
	Family        string `json:"family" binding:"required"`
	SourceType    string `json:"source_type" binding:"required"`
	SourceURI     string `json:"source_uri" binding:"required"`
	Description   string `json:"description"`
	VersionName   string `json:"version_name"`
	Revision      string `json:"revision"`
	ParameterSize string `json:"parameter_size"`
	Precision     string `json:"precision"`
}

func (h *ModelHandler) CreateModel(c *gin.Context) {
	var req CreateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid request body",
			"details": err.Error(),
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Family = strings.TrimSpace(req.Family)
	req.SourceType = strings.TrimSpace(req.SourceType)
	req.SourceURI = strings.TrimSpace(req.SourceURI)

	if req.VersionName == "" {
		req.VersionName = "default"
	}
	if req.Revision == "" {
		req.Revision = "main"
	}
	if req.Precision == "" {
		req.Precision = "auto"
	}

	if !isSupportedSourceType(req.SourceType) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "unsupported source_type",
			"supported_source_types": []string{
				"huggingface",
				"modelscope",
				"local",
				"minio",
			},
		})
		return
	}

	model := domain.Model{
		Name:        req.Name,
		Family:      req.Family,
		SourceType:  req.SourceType,
		SourceURI:   req.SourceURI,
		Description: req.Description,
		Versions: []domain.ModelVersion{
			{
				VersionName:   req.VersionName,
				Revision:      req.Revision,
				ParameterSize: req.ParameterSize,
				Precision:     req.Precision,
				Status:        "CREATED",
			},
		},
	}

	if err := h.db.Create(&model).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to create model",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, model)
}

func (h *ModelHandler) ListModels(c *gin.Context) {
	var models []domain.Model

	if err := h.db.
		Preload("Versions").
		Order("id desc").
		Find(&models).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to list models",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items": models,
		"total": len(models),
	})
}

func (h *ModelHandler) GetModel(c *gin.Context) {
	id := c.Param("id")

	var model domain.Model
	if err := h.db.
		Preload("Versions").
		First(&model, id).Error; err != nil {
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

	c.JSON(http.StatusOK, model)
}

func isSupportedSourceType(sourceType string) bool {
	switch sourceType {
	case "huggingface", "modelscope", "local", "minio":
		return true
	default:
		return false
	}
}
