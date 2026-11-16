package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestGenerate(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected Authorization header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "gpt-image-test" || body["prompt"] != "draw a cat" {
			t.Fatalf("unexpected body: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"output_format":"png","data":[{"b64_json":"`+base64.StdEncoding.EncodeToString(tinyPNG)+`"}]}`)
	}))
	defer server.Close()

	client := newImageClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", DefaultModel: "gpt-image-test", Timeout: time.Second})
	images, model, err := client.Generate(context.Background(), "draw a cat", ImageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if model != "gpt-image-test" || len(images) != 1 || images[0].MIMEType != "image/png" {
		t.Fatalf("unexpected result: model=%s images=%#v", model, images)
	}
}

func TestEditUsesImageArrayMultipartField(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	one := filepath.Join(tmp, "one.png")
	two := filepath.Join(tmp, "two.png")
	if err := os.WriteFile(one, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		var imageFields []string
		var imageContentTypes []string
		fields := map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(part)
			if part.FileName() != "" {
				imageFields = append(imageFields, part.FormName())
				imageContentTypes = append(imageContentTypes, part.Header.Get("Content-Type"))
			} else {
				fields[part.FormName()] = string(data)
			}
		}
		if !slices.Equal(imageFields, []string{"image[]", "image[]"}) {
			t.Fatalf("unexpected multipart image fields: %#v", imageFields)
		}
		if !slices.Equal(imageContentTypes, []string{"image/png", "image/png"}) {
			t.Fatalf("unexpected multipart image content types: %#v", imageContentTypes)
		}
		if fields["model"] != "gpt-image-test" || fields["prompt"] != "combine them" {
			t.Fatalf("unexpected fields: %#v", fields)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"output_format":"png","data":[{"b64_json":"`+base64.StdEncoding.EncodeToString(tinyPNG)+`"}]}`)
	}))
	defer server.Close()

	client := newImageClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", DefaultModel: "gpt-image-test", Timeout: time.Second})
	images, _, err := client.Edit(context.Background(), "combine them", []string{one, two}, "", ImageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
}


func TestValidateImagePathDetectsMIMEFromContent(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cases := []struct {
		name       string
		fileName   string
		wantName   string
		wantMIME   string
	}{
		{name: "no extension", fileName: "reference", wantName: "reference.png", wantMIME: "image/png"},
		{name: "wrong extension", fileName: "reference.bin", wantName: "reference.png", wantMIME: "image/png"},
		{name: "matching extension", fileName: "reference.png", wantName: "reference.png", wantMIME: "image/png"},
	}

	client := newImageClient(Config{})
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmp, tc.fileName)
			if err := os.WriteFile(path, tinyPNG, 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := client.validateImagePath(path, maxReferenceImageBytes, false)
			if err != nil {
				t.Fatal(err)
			}
			if got.MIMEType != tc.wantMIME {
				t.Fatalf("MIMEType = %q, want %q", got.MIMEType, tc.wantMIME)
			}
			if got.Name != tc.wantName {
				t.Fatalf("Name = %q, want %q", got.Name, tc.wantName)
			}
		})
	}
}

func TestWriteMultipartFileUsesDetectedImageMIME(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "upload.bin")
	if err := os.WriteFile(path, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}

	client := newImageClient(Config{})
	file, err := client.validateImagePath(path, maxReferenceImageBytes, false)
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := writeMultipartFile(mw, "image[]", file); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	reader, err := req.MultipartReader()
	if err != nil {
		t.Fatal(err)
	}
	part, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if got := part.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := part.FileName(); got != "upload.png" {
		t.Fatalf("filename = %q, want upload.png", got)
	}
}
