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

// UploadStream writes a public object from a streaming reader. Use this
// for large files such as long-form recordings that should not be buffered
// into process memory before being sent to Spaces.
func UploadStream(key string, body io.Reader, contentType string, size int64) (string, error) {
	if client == nil {
		return "", fmt.Errorf("spaces not configured")
	}
	input := &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         body,
		ContentType:  aws.String(contentType),
		CacheControl: aws.String("public, max-age=300"),
		ACL:          s3types.ObjectCannedACLPublicRead,
	}
	if size > 0 {
		input.ContentLength = aws.Int64(size)
	}
	_, err := client.PutObject(context.Background(), input)
	if err != nil {
		return "", fmt.Errorf("failed to upload %s: %w", key, err)
	}
	return PublicURL(key), nil
}

// PutPrivate writes an object without a public-read ACL. Use this for
// encrypted credentials or browser-profile archives; callers are still
// responsible for encrypting sensitive bytes before handing them here.
func PutPrivate(key string, data []byte, contentType string) error {
	if client == nil {
		return fmt.Errorf("spaces not configured")
	}
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("failed to upload private object %s: %w", key, err)
	}
	return nil
}

// DeletePrivate removes a private object. It is intentionally narrow:
// public assets should continue to flow through the purpose-built
// upload/update paths above.
func DeletePrivate(key string) error {
	if client == nil {
		return fmt.Errorf("spaces not configured")
	}
	_, err := client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete private object %s: %w", key, err)
	}
	return nil
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

// TalkManifestKey is the location of the per-clipart fingerprint
// manifest. Maintained by upload-talk-cliparts so mediagen can pick up
// "the bytes behind talk.Clipart changed" without needing the file
// available locally.
const TalkManifestKey = "talks/_manifest.json"

// SpeakerManifestKey is the location of the per-speaker-photo
// fingerprint manifest. Maintained by the speaker photo upload path so
// media-card hashing can stop depending on static/img/speakers.
const SpeakerManifestKey = "speakers/_manifest.json"

// LoadJSONMap reads a JSON map (string→string) from the given Spaces
// key. Returns an empty map when the key doesn't exist yet (so a
// caller can use it to bootstrap a fresh manifest without special-
// casing). Generalizes LoadHashes so other indexes (talks manifest,
// speaker manifest, …) can reuse the same plumbing.
func LoadJSONMap(key string) (map[string]string, error) {
	if client == nil {
		return nil, fmt.Errorf("spaces not configured")
	}
	result, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return make(map[string]string), nil
	}
	defer result.Body.Close()
	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	out := make(map[string]string)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", key, err)
	}
	return out, nil
}

// SaveJSONMap writes a JSON map (string→string) to the given Spaces
// key. Public-read so callers serving via the same bucket can pull
// it without signed URLs.
func SaveJSONMap(key string, m map[string]string) error {
	if client == nil {
		return fmt.Errorf("spaces not configured")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
		ACL:         s3types.ObjectCannedACLPublicRead,
	})
	return err
}

// ListKeys returns all object keys under prefix. It follows S3 pagination
// so callers can safely use it for large asset folders.
func ListKeys(prefix string) ([]string, error) {
	if client == nil {
		return nil, fmt.Errorf("spaces not configured")
	}
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.Background())
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			keys = append(keys, *obj.Key)
		}
	}
	return keys, nil
}

// Get fetches an object's raw bytes by key. Used by the admin
// social-cards download to stream a zip of the per-conf 1080p PNGs
// without going through the public CDN. Returns a 404-style error
// (NotFound from the SDK) when the key isn't in the bucket; callers
// should treat that as "skip this entry" rather than fatal.
func Get(key string) ([]byte, error) {
	if client == nil {
		return nil, fmt.Errorf("spaces not configured")
	}
	result, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer result.Body.Close()
	return io.ReadAll(result.Body)
}

// GetStream returns a streaming reader + content length for an object,
// without buffering the body. Required by the YouTube uploader so a
// multi-GB longform video doesn't have to fit in process memory.
// Caller must Close the returned ReadCloser.
//
// Returns ErrNotConfigured when Init hasn't been called; an S3
// NotFound-style error when the key doesn't exist; or a length of -1
// when the bucket didn't return Content-Length (some S3-compatible
// stores omit it on chunked transfers — callers should treat -1 as
// "unknown" and pass it through to the uploader as-is).
func GetStream(key string) (io.ReadCloser, int64, error) {
	if client == nil {
		return nil, 0, fmt.Errorf("spaces not configured")
	}
	result, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, 0, err
	}
	var size int64 = -1
	if result.ContentLength != nil {
		size = *result.ContentLength
	}
	return result.Body, size, nil
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
