package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strconv"
	"strings"
)

const (
	maxPromptRunes             = 32000
	maxReferenceImages         = 16
	maxReferenceImageBytes     = int64(50 << 20)
	maxMaskBytes               = int64(4 << 20)
	maxAPIResponseBytes        = int64(512 << 20)
	maxDownloadedImageBytes    = int64(100 << 20)
	defaultGeneratedImageCount = 1
)

type ImageClient struct {
	cfg                 Config
	capabilities        ProviderCapabilities
	httpClient          *http.Client
	referenceHTTPClient *http.Client
}

type ImageOptions struct {
	Model             string
	Size              string
	Quality           string
	N                 int
	Transparent       bool
	Background        string
	OutputFormat      string
	Complexity        string
	InputFidelity     string
	OutputCompression *int
}

type ImageResponse struct {
	Created      int64  `json:"created"`
	Background   string `json:"background"`
	OutputFormat string `json:"output_format"`
	Quality      string `json:"quality"`
	Size         string `json:"size"`
	Data         []struct {
		B64JSON       string `json:"b64_json"`
		URL           string `json:"url"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
}

type GeneratedImage struct {
	Bytes         []byte
	MIMEType      string
	Extension     string
	RevisedPrompt string
}

type validatedFile struct {
	Path     string
	Name     string
	Size     int64
	MIMEType string
}

func newImageClient(cfg Config) *ImageClient {
	return &ImageClient{
		cfg:          cfg,
		capabilities: providerCapabilities(cfg),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		referenceHTTPClient: newReferenceHTTPClient(cfg),
	}
}

func (c *ImageClient) Generate(ctx context.Context, prompt string, opts ImageOptions) ([]GeneratedImage, string, error) {
	if err := c.validateCommon(prompt, &opts, false); err != nil {
		return nil, "", err
	}

	payload := map[string]any{
		"model":  opts.Model,
		"prompt": strings.TrimSpace(prompt),
	}
	if c.capabilities.N {
		payload["n"] = opts.N
	}
	addOptionalFields(payload, opts, false, c.capabilities)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("encode generation request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("create generation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("image generation request failed: %w", err)
	}
	defer resp.Body.Close()

	parsed, err := decodeImageResponse(resp)
	if err != nil {
		return nil, "", err
	}
	images, err := c.materializeImages(ctx, parsed)
	if err != nil {
		return nil, "", err
	}
	return images, opts.Model, nil
}

func (c *ImageClient) Edit(ctx context.Context, prompt string, imageSources []string, maskSource string, opts ImageOptions) ([]GeneratedImage, string, error) {
	if err := c.validateCommon(prompt, &opts, true); err != nil {
		return nil, "", err
	}
	if len(imageSources) == 0 || len(imageSources) > maxReferenceImages {
		return nil, "", &InputError{Parameter: "reference_images", Message: fmt.Sprintf("must contain between 1 and %d images", maxReferenceImages)}
	}

	tempDir, err := os.MkdirTemp("", "gpt-image-mcp-input-*")
	if err != nil {
		return nil, "", fmt.Errorf("create temporary input directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	refs := make([]validatedFile, 0, len(imageSources))
	for _, source := range imageSources {
		file, err := c.resolveImageSource(ctx, source, maxReferenceImageBytes, false, tempDir)
		if err != nil {
			return nil, "", err
		}
		refs = append(refs, file)
	}

	var mask *validatedFile
	if strings.TrimSpace(maskSource) != "" {
		file, err := c.resolveImageSource(ctx, maskSource, maxMaskBytes, true, tempDir)
		if err != nil {
			return nil, "", fmt.Errorf("mask: %w", err)
		}
		mask = &file
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/images/edits", pr)
	if err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, "", fmt.Errorf("create edit request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.setHeaders(req)

	go func() {
		writeErr := writeEditMultipart(mw, strings.TrimSpace(prompt), refs, mask, opts, c.capabilities)
		if closeErr := mw.Close(); writeErr == nil {
			writeErr = closeErr
		}
		_ = pw.CloseWithError(writeErr)
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		_ = pr.CloseWithError(err)
		return nil, "", fmt.Errorf("image edit request failed: %w", err)
	}
	defer resp.Body.Close()

	parsed, err := decodeImageResponse(resp)
	if err != nil {
		return nil, "", err
	}
	images, err := c.materializeImages(ctx, parsed)
	if err != nil {
		return nil, "", err
	}
	return images, opts.Model, nil
}

func (c *ImageClient) validateCommon(prompt string, opts *ImageOptions, editing bool) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return &InputError{Parameter: "prompt", Message: "is required"}
	}
	if len([]rune(prompt)) > maxPromptRunes {
		return &InputError{Parameter: "prompt", Message: fmt.Sprintf("exceeds %d characters", maxPromptRunes)}
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = c.cfg.DefaultModel
	}
	if strings.TrimSpace(opts.Model) == "" {
		return &InputError{Parameter: "model", Message: "no image model is configured"}
	}
	if opts.N == 0 {
		opts.N = defaultGeneratedImageCount
	}
	if opts.N < 1 || opts.N > 10 {
		return &InputError{Parameter: "n", Message: "must be between 1 and 10"}
	}
	if opts.N != 1 && !c.capabilities.N {
		return &InputError{Parameter: "n", Message: fmt.Sprintf("provider profile %q does not support requesting multiple images", c.capabilities.Name)}
	}
	if opts.Size != "" && !c.capabilities.Size {
		return &InputError{Parameter: "size", Message: fmt.Sprintf("provider profile %q does not expose size", c.capabilities.Name)}
	}
	if opts.Quality != "" && !c.capabilities.Quality {
		return &InputError{Parameter: "quality", Message: fmt.Sprintf("provider profile %q does not expose quality", c.capabilities.Name)}
	}
	if opts.Transparent {
		if !c.capabilities.Background || !c.capabilities.OutputFormat {
			return &InputError{Parameter: "transparent", Message: fmt.Sprintf("provider profile %q does not expose transparent output", c.capabilities.Name)}
		}
		opts.Background = "transparent"
		opts.OutputFormat = "png"
	}

	if c.capabilities.Background && opts.Background == "" {
		opts.Background = c.cfg.Defaults.Background
	}
	if c.capabilities.OutputFormat && opts.OutputFormat == "" {
		opts.OutputFormat = c.cfg.Defaults.OutputFormat
	}
	if c.capabilities.Complexity {
		opts.Complexity = c.cfg.Defaults.Complexity
	}
	if editing && c.capabilities.InputFidelity {
		opts.InputFidelity = c.cfg.Defaults.InputFidelity
	}
	if c.capabilities.OutputCompression {
		opts.OutputCompression = c.cfg.Defaults.OutputCompression
	}
	if opts.OutputCompression != nil && (*opts.OutputCompression < 0 || *opts.OutputCompression > 100) {
		return &InputError{Parameter: "output_compression", Message: "must be between 0 and 100"}
	}
	if opts.Background == "transparent" && opts.OutputFormat != "" && opts.OutputFormat != "png" && opts.OutputFormat != "webp" {
		return &InputError{Parameter: "output_format", Message: "transparent background requires png or webp"}
	}
	return nil
}

func addOptionalFields(payload map[string]any, opts ImageOptions, editing bool, caps ProviderCapabilities) {
	if caps.Size && opts.Size != "" {
		payload["size"] = opts.Size
	}
	if caps.Quality && opts.Quality != "" {
		payload["quality"] = opts.Quality
	}
	if caps.Background && opts.Background != "" {
		payload["background"] = opts.Background
	}
	if caps.OutputFormat && opts.OutputFormat != "" {
		payload["output_format"] = opts.OutputFormat
	}
	if caps.Complexity && opts.Complexity != "" {
		payload["complexity"] = opts.Complexity
	}
	if editing && caps.InputFidelity && opts.InputFidelity != "" {
		payload["input_fidelity"] = opts.InputFidelity
	}
	if caps.OutputCompression && opts.OutputCompression != nil {
		payload["output_compression"] = *opts.OutputCompression
	}
}

func writeEditMultipart(mw *multipart.Writer, prompt string, refs []validatedFile, mask *validatedFile, opts ImageOptions, caps ProviderCapabilities) error {
	fields := map[string]string{
		"model":  opts.Model,
		"prompt": prompt,
	}
	if caps.N {
		fields["n"] = strconv.Itoa(opts.N)
	}
	if caps.Size && opts.Size != "" {
		fields["size"] = opts.Size
	}
	if caps.Quality && opts.Quality != "" {
		fields["quality"] = opts.Quality
	}
	if caps.Background && opts.Background != "" {
		fields["background"] = opts.Background
	}
	if caps.OutputFormat && opts.OutputFormat != "" {
		fields["output_format"] = opts.OutputFormat
	}
	if caps.Complexity && opts.Complexity != "" {
		fields["complexity"] = opts.Complexity
	}
	if caps.InputFidelity && opts.InputFidelity != "" {
		fields["input_fidelity"] = opts.InputFidelity
	}
	if caps.OutputCompression && opts.OutputCompression != nil {
		fields["output_compression"] = strconv.Itoa(*opts.OutputCompression)
	}

	for key, value := range fields {
		if err := mw.WriteField(key, value); err != nil {
			return fmt.Errorf("write multipart field %s: %w", key, err)
		}
	}
	for _, ref := range refs {
		if err := writeMultipartFile(mw, "image[]", ref); err != nil {
			return err
		}
	}
	if mask != nil {
		if err := writeMultipartFile(mw, "mask", *mask); err != nil {
			return err
		}
	}
	return nil
}

func writeMultipartFile(mw *multipart.Writer, field string, file validatedFile) error {
	src, err := os.Open(file.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", file.Path, err)
	}
	defer src.Close()

	disposition := mime.FormatMediaType("form-data", map[string]string{
		"name":     field,
		"filename": file.Name,
	})
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", disposition)
	header.Set("Content-Type", file.MIMEType)

	part, err := mw.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create multipart file %s: %w", file.Name, err)
	}
	if _, err := io.Copy(part, src); err != nil {
		return fmt.Errorf("stream multipart file %s: %w", file.Name, err)
	}
	return nil
}

func (c *ImageClient) setHeaders(req *http.Request) {
	if strings.TrimSpace(c.cfg.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	if c.cfg.Organization != "" {
		req.Header.Set("OpenAI-Organization", c.cfg.Organization)
	}
	if c.cfg.Project != "" {
		req.Header.Set("OpenAI-Project", c.cfg.Project)
	}
	for key, value := range c.cfg.ExtraHeaders {
		if strings.TrimSpace(key) != "" {
			req.Header.Set(key, value)
		}
	}
}

func decodeImageResponse(resp *http.Response) (ImageResponse, error) {
	body := io.LimitReader(resp.Body, maxAPIResponseBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		return ImageResponse{}, fmt.Errorf("read image API response: %w", err)
	}
	if int64(len(data)) > maxAPIResponseBytes {
		return ImageResponse{}, fmt.Errorf("image API response exceeded %d MiB", maxAPIResponseBytes>>20)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ImageResponse{}, apiError(resp.StatusCode, data)
	}

	var parsed ImageResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ImageResponse{}, fmt.Errorf("decode image API response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return ImageResponse{}, errors.New("image API returned no images")
	}
	return parsed, nil
}

func apiError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		return &APIError{
			Status:  status,
			Message: envelope.Error.Message,
			Type:    envelope.Error.Type,
			Code:    fmt.Sprint(envelope.Error.Code),
		}
	}
	message := strings.TrimSpace(string(body))
	if len(message) > 1000 {
		message = message[:1000] + "…"
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return &APIError{Status: status, Message: message}
}

func (c *ImageClient) materializeImages(ctx context.Context, response ImageResponse) ([]GeneratedImage, error) {
	result := make([]GeneratedImage, 0, len(response.Data))
	for i, item := range response.Data {
		var imageBytes []byte
		var err error
		switch {
		case item.B64JSON != "":
			if int64(base64.StdEncoding.DecodedLen(len(item.B64JSON))) > maxDownloadedImageBytes {
				return nil, fmt.Errorf("image %d exceeds %d MiB decoded size limit", i+1, maxDownloadedImageBytes>>20)
			}
			imageBytes, err = base64.StdEncoding.DecodeString(item.B64JSON)
			if err != nil {
				return nil, fmt.Errorf("decode image %d: %w", i+1, err)
			}
		case item.URL != "":
			imageBytes, err = c.downloadImage(ctx, item.URL)
			if err != nil {
				return nil, fmt.Errorf("download image %d: %w", i+1, err)
			}
		default:
			return nil, fmt.Errorf("image %d has neither b64_json nor url", i+1)
		}

		mimeType, ext, err := detectOutputType(imageBytes)
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i+1, err)
		}
		result = append(result, GeneratedImage{
			Bytes:         imageBytes,
			MIMEType:      mimeType,
			Extension:     ext,
			RevisedPrompt: item.RevisedPrompt,
		})
	}
	return result, nil
}

func (c *ImageClient) downloadImage(ctx context.Context, imageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadedImageBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxDownloadedImageBytes {
		return nil, fmt.Errorf("download exceeded %d MiB", maxDownloadedImageBytes>>20)
	}
	return data, nil
}

func detectOutputType(data []byte) (mimeType, ext string, err error) {
	if len(data) == 0 {
		return "", "", errors.New("image data is empty")
	}
	mimeType = http.DetectContentType(data)
	switch mimeType {
	case "image/png":
		return mimeType, ".png", nil
	case "image/jpeg":
		return mimeType, ".jpg", nil
	case "image/webp":
		return mimeType, ".webp", nil
	case "image/gif":
		return mimeType, ".gif", nil
	}

	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "<svg") || (strings.HasPrefix(trimmed, "<?xml") && strings.Contains(trimmed[:min(len(trimmed), 1024)], "<svg")) {
		return "image/svg+xml", ".svg", nil
	}
	return "", "", fmt.Errorf("API returned non-image content of type %q", mimeType)
}
