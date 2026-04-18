package spaces

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var (
	client   *s3.Client
	bucket   string
	endpoint string
)

func Init(cfg types.SpacesConfig) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.Key == "" || cfg.Secret == "" {
		return
	}

	endpoint = cfg.Endpoint
	bucket = cfg.Bucket

	awsCfg := aws.Config{
		Region: cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.Key, cfg.Secret, "",
		),
	}

	client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = false
	})
}

func IsConfigured() bool {
	return client != nil
}

func Upload(key string, data []byte, contentType string, hash string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("spaces not configured")
	}

	metadata := map[string]string{}
	if hash != "" {
		metadata["card-hash"] = hash
	}

	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         bytes.NewReader(data),
		ContentType:  aws.String(contentType),
		CacheControl: aws.String("public, max-age=300"),
		ACL:          s3types.ObjectCannedACLPublicRead,
		Metadata:     metadata,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload %s: %w", key, err)
	}

	return PublicURL(key), nil
}

// LoadHashes lists all objects in the bucket and reads their card-hash
// metadata, returning a map of S3 key -> hash.
func LoadHashes() (map[string]string, error) {
	if client == nil {
		return nil, fmt.Errorf("spaces not configured")
	}

	hashes := make(map[string]string)

	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)

			head, err := client.HeadObject(context.Background(), &s3.HeadObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(key),
			})
			if err != nil {
				continue
			}

			if hash, ok := head.Metadata["card-hash"]; ok {
				hashes[key] = hash
			}
		}
	}

	return hashes, nil
}

func PublicURL(key string) string {
	// DO Spaces uses subdomain-style URLs for public access
	// https://nyc3.digitaloceanspaces.com -> https://bucket.nyc3.digitaloceanspaces.com
	base := strings.Replace(endpoint, "https://", fmt.Sprintf("https://%s.", bucket), 1)
	return fmt.Sprintf("%s/%s", base, key)
}
