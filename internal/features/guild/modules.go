package guild

import (
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
	"github.com/minio/minio-go/v7"
	"go.uber.org/fx"
)

func provideService(repo *Repository, pubsub *realtime.LPubSub, client *authz.Client) *Service {
	return NewService(repo, pubsub, client)
}

type dependencies struct {
	fx.In

	Config      *config.Config
	MinIO       *minio.Client `name:"minio"`
	MinIOSigner *minio.Client `name:"minioSigner"`
	Service     *Service
}

func wireDependencies(p dependencies) {
	p.Service.SetStorage(p.MinIO, p.MinIOSigner, p.Config.MinIO.Bucket)
	p.Service.SetInviteBaseURL(p.Config.InviteBaseURL)
}

var Module = fx.Module("guild",
	fx.Provide(NewRepository, provideService, NewHandler),
	fx.Invoke(wireDependencies),
)
