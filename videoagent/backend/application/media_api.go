package application

import (
	"net/http"

	"eino-cli/videoagent/backend/media"
)

type (
	ArkImageConfig       = media.ArkImageConfig
	ArkImageClient       = media.ArkImageClient
	StoredAudio          = media.StoredAudio
	AudioImporter        = media.AudioImporter
	AudioUploader        = media.AudioUploader
	HTTPAudioImporter    = media.HTTPAudioImporter
	AudioStorageConfig   = media.AudioStorageConfig
	MediaURLConfig       = media.MediaURLConfig
	MetaFinalVideoConfig = media.MetaFinalVideoConfig
	MetaFinalVideoClient = media.MetaFinalVideoClient
	PromptTTSConfig      = media.PromptTTSConfig
	PromptTTSClient      = media.PromptTTSClient
	SeedanceConfig       = media.SeedanceConfig
	SeedanceClient       = media.SeedanceClient
	MediaURLResolvers    = media.MediaURLResolvers
	HTTPVideoImporter    = media.HTTPVideoImporter
	VideoStorageConfig   = media.VideoStorageConfig
	RemoteConfig         = media.RemoteConfig
)

func NewArkImageClient(config ArkImageConfig) (*ArkImageClient, error) {
	return media.NewArkImageClient(config)
}

func NewHTTPAudioImporter(uploader AudioUploader, client *http.Client, maxBytes int64) (*HTTPAudioImporter, error) {
	return media.NewHTTPAudioImporter(uploader, client, maxBytes)
}

func NewBytedanceAudioUploader(config AudioStorageConfig) (AudioUploader, error) {
	return media.NewBytedanceAudioUploader(config)
}

func NewBytedanceMatxClient() (MatxClient, error) {
	return media.NewBytedanceMatxClient()
}

func NewBytedanceMediaURLResolver(config MediaURLConfig) (MediaURLResolver, error) {
	return media.NewBytedanceMediaURLResolver(config)
}

func NewMetaFinalVideoClient(config MetaFinalVideoConfig, renderer VideoRenderer) (*MetaFinalVideoClient, error) {
	return media.NewMetaFinalVideoClient(config, renderer)
}

func NewBytedanceVideoRenderer(bizID int) (VideoRenderer, error) {
	return media.NewBytedanceVideoRenderer(bizID)
}

func NewPromptTTSClient(config PromptTTSConfig, matx MatxClient) (*PromptTTSClient, error) {
	return media.NewPromptTTSClient(config, matx)
}

func NewPromptTTSClientWithImporter(config PromptTTSConfig, matx MatxClient, importer AudioImporter) (*PromptTTSClient, error) {
	return media.NewPromptTTSClientWithImporter(config, matx, importer)
}

func NewSeedanceClient(config SeedanceConfig, client *http.Client) (*SeedanceClient, error) {
	return media.NewSeedanceClient(config, client)
}

func NewSeedanceClientWithImporter(config SeedanceConfig, client *http.Client, importer VideoImporter) (*SeedanceClient, error) {
	return media.NewSeedanceClientWithImporter(config, client, importer)
}

func NewSeedanceClientWithMediaResolver(config SeedanceConfig, client *http.Client, importer VideoImporter, resolver MediaURLResolver) (*SeedanceClient, error) {
	return media.NewSeedanceClientWithMediaResolver(config, client, importer, resolver)
}

func NewSeedanceClientWithMediaResolvers(config SeedanceConfig, client *http.Client, importer VideoImporter, resolvers MediaURLResolvers) (*SeedanceClient, error) {
	return media.NewSeedanceClientWithMediaResolvers(config, client, importer, resolvers)
}

func NewHTTPVideoImporter(uploader VideoUploader, client *http.Client, maxBytes int64, cache VideoImportCache) (*HTTPVideoImporter, error) {
	return media.NewHTTPVideoImporter(uploader, client, maxBytes, cache)
}

func NewBytedanceVideoUploader(config VideoStorageConfig) (VideoUploader, error) {
	return media.NewBytedanceVideoUploader(config)
}

func NewRemoteClients(config RemoteConfig, cache VideoImportCache) (Clients, error) {
	return media.NewRemoteClients(config, cache)
}

func ValidateCanvasRemoteConfig(config RemoteConfig) error {
	return media.ValidateCanvasRemoteConfig(config)
}
