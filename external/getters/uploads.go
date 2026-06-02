package getters

import "btcpp-web/internal/config"

func UploadFile(ctx *config.AppContext, contentType, filename string, data []byte) (string, error) {
	if UsePostgresBackend(ctx) {
		return uploadFilePostgres(contentType, filename, data)
	}
	return uploadFileNotion(ctx.Notion, contentType, filename, data)
}
