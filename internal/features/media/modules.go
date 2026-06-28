package media

import (
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/minio/minio-go/v7"
	"go.uber.org/fx"
)

type serviceParams struct {
	fx.In

	Repository *Repository
	Client     *minio.Client `name:"minio"`
	Signer     *minio.Client `name:"minioSigner"`
	Config     config.MinIOConfig
}

func provideService(p serviceParams) *Service {
	return NewService(p.Repository, p.Client, p.Signer, p.Config)
}

var Module = fx.Module("media",
	fx.Provide(NewRepository, provideService, NewHandler),
)
