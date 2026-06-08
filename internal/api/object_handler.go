package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/WindAdherent/llm-platform/internal/storage"
)

type ObjectHandler struct {
	store *storage.ObjectStorage
}

func NewObjectHandler(store *storage.ObjectStorage) *ObjectHandler {
	return &ObjectHandler{store: store}
}

func (h *ObjectHandler) UploadObject(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "file is required",
			"details": err.Error(),
		})
		return
	}
	defer file.Close()

	category := c.PostForm("category")
	if category == "" {
		category = "uploads"
	}

	category = sanitizePathPart(category)
	filename := sanitizeFilename(header.Filename)
	objectKey := buildObjectKey(category, filename)

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	uploaded, err := h.store.Upload(
		c.Request.Context(),
		objectKey,
		file,
		header.Size,
		contentType,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to upload object",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, uploaded)
}

func (h *ObjectHandler) ListObjects(c *gin.Context) {
	prefix := c.Query("prefix")
	recursive := true

	if raw := c.Query("recursive"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "recursive must be true or false",
			})
			return
		}
		recursive = parsed
	}

	items, err := h.store.List(c.Request.Context(), prefix, recursive)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to list objects",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"total": len(items),
	})
}

func (h *ObjectHandler) PresignedGetObjectURL(c *gin.Context) {
	objectKey := c.Query("object_key")
	if objectKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "object_key is required",
		})
		return
	}

	expiresSeconds := 3600

	if raw := c.Query("expires_seconds"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "expires_seconds must be a positive integer",
			})
			return
		}

		expiresSeconds = parsed
	}

	maxExpiresSeconds := int((7 * 24 * time.Hour).Seconds())
	if expiresSeconds > maxExpiresSeconds {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "expires_seconds cannot be greater than 604800",
		})
		return
	}

	url, err := h.store.PresignedGetURL(
		c.Request.Context(),
		objectKey,
		time.Duration(expiresSeconds)*time.Second,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to generate presigned url",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"object_key":      objectKey,
		"expires_seconds": expiresSeconds,
		"url":             url,
	})
}

func buildObjectKey(category string, filename string) string {
	now := time.Now()
	randomID := randomHex(8)

	return strings.Join([]string{
		category,
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
		randomID + "-" + filename,
	}, "/")
}

func sanitizeFilename(filename string) string {
	base := filepath.Base(filename)
	base = strings.TrimSpace(base)

	if base == "" || base == "." || base == "/" {
		return "unnamed"
	}

	base = strings.ReplaceAll(base, " ", "_")
	base = strings.ReplaceAll(base, "/", "_")
	base = strings.ReplaceAll(base, "\\", "_")

	return base
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, "\\", "-")

	if value == "" {
		return "uploads"
	}

	return value
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().Format("150405000000")
	}

	return hex.EncodeToString(buf)
}
