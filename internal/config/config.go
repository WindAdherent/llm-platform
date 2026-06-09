package config

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AppName string
	AppEnv  string
	AppHost string
	AppPort string

	MySQLHost     string
	MySQLPort     string
	MySQLUser     string
	MySQLPassword string
	MySQLDatabase string

	RedisHost     string
	RedisPort     string
	RedisPassword string

	MinIOEndpoint  string
	MinIOAccessKey string
	MinIOSecretKey string
	MinIOBucket    string
	MinIOUseSSL    string

	ModelDownloadMode      string
	ModelDownloaderCommand string

	RuntimeHost         string
	RuntimeK8SNamespace string
}

func Load() Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	return Config{
		AppName: getEnv("APP_NAME", "llm-platform"),
		AppEnv:  getEnv("APP_ENV", "local"),
		AppHost: getEnv("APP_HOST", "0.0.0.0"),
		AppPort: getEnv("APP_PORT", "8080"),

		MySQLHost:     getEnv("MYSQL_HOST", "47.94.178.189"),
		MySQLPort:     getEnv("MYSQL_PORT", "3306"),
		MySQLUser:     getEnv("MYSQL_USER", "llm_platform"),
		MySQLPassword: getEnv("MYSQL_PASSWORD", "&shieshuyuan21"),
		MySQLDatabase: getEnv("MYSQL_DATABASE", "llm_platform"),

		RedisHost:     getEnv("REDIS_HOST", "127.0.0.1"),
		RedisPort:     getEnv("REDIS_PORT", "6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),

		MinIOEndpoint:  getEnv("MINIO_ENDPOINT", "127.0.0.1:9000"),
		MinIOAccessKey: getEnv("MINIO_ACCESS_KEY", "minioadmin"),
		MinIOSecretKey: getEnv("MINIO_SECRET_KEY", "minioadmin"),
		MinIOBucket:    getEnv("MINIO_BUCKET", "llm-platform"),
		MinIOUseSSL:    getEnv("MINIO_USE_SSL", "false"),

		ModelDownloadMode:      getEnv("MODEL_DOWNLOAD_MODE", "simulated"),
		ModelDownloaderCommand: getEnv("MODEL_DOWNLOADER_COMMAND", "python3 build/model-downloader/downloader.py"),

		RuntimeHost:         getEnv("RUNTIME_HOST", "127.0.0.1"),
		RuntimeK8SNamespace: getEnv("RUNTIME_K8S_NAMESPACE", "llm"),
	}
}

func (c Config) MySQLDSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.MySQLUser,
		c.MySQLPassword,
		c.MySQLHost,
		c.MySQLPort,
		c.MySQLDatabase,
	)
}

func (c Config) HTTPAddr() string {
	return fmt.Sprintf("%s:%s", c.AppHost, c.AppPort)
}

func getEnv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
