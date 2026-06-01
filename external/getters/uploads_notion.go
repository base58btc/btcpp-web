package getters

import (
	"context"
	"fmt"

	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func uploadFileNotion(n *types.Notion, contentType, filename string, data []byte) (string, error) {
	upload, err := n.Client.CreateFileUpload(context.Background())
	if err != nil {
		return "", err
	}

	upload.Filename = filename
	upload.ContentType = contentType
	result, err := n.Client.UploadFile(context.Background(), upload, data)
	if err != nil {
		return "", err
	}

	if result.Status != notion.FileStatusUploaded {
		return "", fmt.Errorf("Unable to upload file. %v", result)
	}

	return result.ID, nil
}
