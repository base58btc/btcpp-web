package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"
)

func multipartUploadRequest(t *testing.T, filename string, body []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("photo", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %s", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("part.Write: %s", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %s", err)
	}
	req, err := http.NewRequest("POST", "/upload", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %s", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestReadMultipartFileRejectsSpoofedImageExtension(t *testing.T) {
	req := multipartUploadRequest(t, "payload.png", []byte("<html><script>alert(1)</script></html>"))

	if err := req.ParseMultipartForm(maxUploadFileBytes); err != nil {
		t.Fatalf("ParseMultipartForm: %s", err)
	}
	if _, _, _, err := readMultipartFile(req, "photo"); err == nil {
		t.Fatal("expected spoofed .png upload to fail")
	}
}

func TestReadMultipartFileAcceptsSniffedPNG(t *testing.T) {
	png := []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01,
	}
	req := multipartUploadRequest(t, "image.bin", png)

	if err := req.ParseMultipartForm(maxUploadFileBytes); err != nil {
		t.Fatalf("ParseMultipartForm: %s", err)
	}
	_, contentType, ext, err := readMultipartFile(req, "photo")
	if err != nil {
		t.Fatalf("readMultipartFile: %s", err)
	}
	if contentType != "image/png" {
		t.Fatalf("contentType = %q, want image/png", contentType)
	}
	if ext != ".png" {
		t.Fatalf("ext = %q, want canonical extension .png", ext)
	}
}

func TestReadMultipartFileAcceptsAVIFByBrand(t *testing.T) {
	avif := []byte{
		0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p',
		'a', 'v', 'i', 'f', 0x00, 0x00, 0x00, 0x00,
		'a', 'v', 'i', 'f',
	}
	req := multipartUploadRequest(t, "image.avif", avif)

	if err := req.ParseMultipartForm(maxUploadFileBytes); err != nil {
		t.Fatalf("ParseMultipartForm: %s", err)
	}
	_, contentType, ext, err := readMultipartFile(req, "photo")
	if err != nil {
		t.Fatalf("readMultipartFile: %s", err)
	}
	if contentType != "image/avif" {
		t.Fatalf("contentType = %q, want image/avif", contentType)
	}
	if ext != ".avif" {
		t.Fatalf("ext = %q, want .avif", ext)
	}
}
