package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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

	client := newImageClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", DefaultModel: "gpt-image-test", Timeout: time.Second, Profile: "compatible"})
	images, model, err := client.Generate(context.Background(), "draw a cat", ImageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if model != "gpt-image-test" || len(images) != 1 || images[0].MIMEType != "image/png" {
		t.Fatalf("unexpected result: model=%s images=%#v", model, images)
	}
}

func TestGenerateAllowsNoAPIKeyAndCustomHeaders(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected Authorization header: %q", got)
		}
		if got := r.Header.Get("X-Local-Auth"); got != "secret" {
			t.Fatalf("missing custom header: %q", got)
		}
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+base64.StdEncoding.EncodeToString(tinyPNG)+`"}]}`)
	}))
	defer server.Close()

	client := newImageClient(Config{
		BaseURL:      server.URL,
		DefaultModel: "local-image-model",
		Timeout:      time.Second,
		Profile:      "minimal",
		ExtraHeaders: map[string]string{"X-Local-Auth": "secret"},
	})
	if _, _, err := client.Generate(context.Background(), "draw a cat", ImageOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibleProfileFiltersAdvancedDefaults(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"background", "output_format", "complexity", "input_fidelity", "output_compression"} {
			if _, ok := body[key]; ok {
				t.Fatalf("compatible profile unexpectedly sent %s: %#v", key, body)
			}
		}
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+base64.StdEncoding.EncodeToString(tinyPNG)+`"}]}`)
	}))
	defer server.Close()

	compression := 80
	client := newImageClient(Config{
		BaseURL:      server.URL,
		DefaultModel: "test",
		Timeout:      time.Second,
		Profile:      "compatible",
		Defaults: ImageDefaults{
			Background:        "transparent",
			OutputFormat:      "png",
			Complexity:        "high",
			InputFidelity:     "high",
			OutputCompression: &compression,
		},
	})
	if _, _, err := client.Generate(context.Background(), "draw", ImageOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestMinimalProfileRejectsUnsupportedAgentOption(t *testing.T) {
	t.Parallel()
	client := newImageClient(Config{BaseURL: "http://example.invalid", DefaultModel: "test", Profile: "minimal"})
	_, _, err := client.Generate(context.Background(), "draw", ImageOptions{Size: "1024x1024"})
	var inputErr *InputError
	if !errors.As(err, &inputErr) || inputErr.Parameter != "size" {
		t.Fatalf("expected size InputError, got %v", err)
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
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+base64.StdEncoding.EncodeToString(tinyPNG)+`"}]}`)
	}))
	defer server.Close()

	client := newImageClient(Config{BaseURL: server.URL + "/v1", DefaultModel: "gpt-image-test", Timeout: time.Second, Profile: "compatible"})
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
		name     string
		fileName string
		wantName string
		wantMIME string
	}{
		{name: "no extension", fileName: "reference", wantName: "reference.png", wantMIME: "image/png"},
		{name: "wrong extension", fileName: "reference.bin", wantName: "reference.png", wantMIME: "image/png"},
		{name: "matching extension", fileName: "reference.png", wantName: "reference.png", wantMIME: "image/png"},
	}

	client := newImageClient(Config{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmp, tc.fileName)
			if err := os.WriteFile(path, tinyPNG, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := client.validateImagePath(path, maxReferenceImageBytes, false)
			if err != nil {
				t.Fatal(err)
			}
			if got.MIMEType != tc.wantMIME || got.Name != tc.wantName {
				t.Fatalf("got MIME=%q name=%q, want MIME=%q name=%q", got.MIMEType, got.Name, tc.wantMIME, tc.wantName)
			}
		})
	}
}

func TestAllowedDirsRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges on Windows")
	}
	t.Parallel()
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.png")
	if err := os.WriteFile(secret, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	client := newImageClient(Config{AllowedDirs: []string{allowed}})
	if _, err := client.validateImagePath(filepath.Join(link, "secret.png"), maxReferenceImageBytes, false); err == nil || !strings.Contains(err.Error(), "outside GPT_IMAGE_ALLOWED_DIRS") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestDataURIReference(t *testing.T) {
	t.Parallel()
	client := newImageClient(Config{})
	temp := t.TempDir()
	source := "data:image/png;base64," + base64.StdEncoding.EncodeToString(tinyPNG)
	file, err := client.resolveImageSource(context.Background(), source, maxReferenceImageBytes, false, temp)
	if err != nil {
		t.Fatal(err)
	}
	if file.MIMEType != "image/png" || file.Name != "image.png" {
		t.Fatalf("unexpected resolved file: %#v", file)
	}
}

func TestRemoteReferenceBlocksPrivateNetworkByDefault(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tinyPNG)
	}))
	defer server.Close()

	client := newImageClient(Config{Timeout: time.Second, AllowRemoteImages: true})
	_, err := client.resolveImageSource(context.Background(), server.URL+"/image.png", maxReferenceImageBytes, false, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "private or local network") {
		t.Fatalf("expected private-network rejection, got %v", err)
	}
}

func TestRemoteReferenceCanOptIntoPrivateNetwork(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tinyPNG)
	}))
	defer server.Close()

	client := newImageClient(Config{Timeout: time.Second, AllowRemoteImages: true, AllowPrivateRemoteImages: true})
	file, err := client.resolveImageSource(context.Background(), server.URL+"/image.png", maxReferenceImageBytes, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if file.MIMEType != "image/png" {
		t.Fatalf("unexpected MIME type: %s", file.MIMEType)
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

func TestMaterializeRejectsNonImageContent(t *testing.T) {
	t.Parallel()
	client := newImageClient(Config{})
	var response ImageResponse
	response.Data = append(response.Data, struct {
		B64JSON       string `json:"b64_json"`
		URL           string `json:"url"`
		RevisedPrompt string `json:"revised_prompt"`
	}{B64JSON: base64.StdEncoding.EncodeToString([]byte("<html>not an image</html>"))})
	if _, err := client.materializeImages(context.Background(), response); err == nil {
		t.Fatal("expected non-image content to be rejected")
	}
}

func TestProviderCapabilitiesAuto(t *testing.T) {
	t.Parallel()
	if got := providerCapabilities(Config{BaseURL: "https://api.openai.com/v1", Profile: "auto"}).Name; got != "openai" {
		t.Fatalf("OpenAI endpoint profile = %q", got)
	}
	if got := providerCapabilities(Config{BaseURL: "https://gateway.example/v1", Profile: "auto"}).Name; got != "compatible" {
		t.Fatalf("gateway profile = %q", got)
	}
}

func TestClassifyAPIError(t *testing.T) {
	t.Parallel()
	detail := classifyToolError(&APIError{Status: http.StatusTooManyRequests, Message: "slow down"})
	if detail.Code != "provider_busy" || !detail.Retryable {
		t.Fatalf("unexpected detail: %#v", detail)
	}
}
