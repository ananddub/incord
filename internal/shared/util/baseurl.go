package util

import (
	"fmt"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
)

func GetMinioBaseURL() string {
	cfg := config.Load()
	return fmt.Sprintf(
		"http://%s/%s/",
		cfg.MinIO.PublicEndpoint,
		cfg.MinIO.Bucket,
	)
}

func GetBaseURl(key string) string {
	return GetMinioBaseURL() + key
}
