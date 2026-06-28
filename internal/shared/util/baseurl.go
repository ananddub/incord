package util

import (
	"fmt"
	"strings"

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
	if strings.HasPrefix(key,"http") || strings.HasPrefix(key,"https") {
		return key	
	}
	return GetMinioBaseURL() + key
}
