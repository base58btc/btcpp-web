package spaces

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

const hashIndexKey = "_hashes.json"

// LoadHashes reads the hash index file from the bucket.
func LoadHashes() (map[string]string, error) {
	if client == nil {
		return nil, fmt.Errorf("spaces not configured")
	}

	result, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(hashIndexKey),
	})
	if err != nil {
		// File doesn't exist yet — return empty map
		return make(map[string]string), nil
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read hash index: %w", err)
	}

	hashes := make(map[string]string)
	if err := json.Unmarshal(data, &hashes); err != nil {
		return nil, fmt.Errorf("failed to parse hash index: %w", err)
	}

	return hashes, nil
}

// SaveHashes writes the hash index file to the bucket.
func SaveHashes(hashes map[string]string) error {
	if client == nil {
		return fmt.Errorf("spaces not configured")
	}

	data, err := json.Marshal(hashes)
	if err != nil {
		return err
	}

	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(hashIndexKey),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	return err
}

// Exists checks if an object exists in the bucket
func Exists(key string) bool {
	if client == nil {
		return false
	}
	_, err := client.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	return err == nil
}

// BaseURL returns the public base URL for the bucket (e.g. https://btcpp.nyc3.digitaloceanspaces.com)
func BaseURL() string {
	if endpoint == "" || bucket == "" {
		return ""
	}
	return strings.Replace(endpoint, "https://", fmt.Sprintf("https://%s.", bucket), 1)
}

func PublicURL(key string) string {
	return fmt.Sprintf("%s/%s", BaseURL(), key)
}
