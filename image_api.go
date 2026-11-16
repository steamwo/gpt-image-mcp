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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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
	cfg        Config
	httpClient *http.Client
}

type ImageOptions struct {
	Model             string
	Size              string
	Quality           string
	Background        string
	OutputFormat      string
	Complexity        string
	InputFidelity     string
	SafetyIdentifier  string
	N                 int
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
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

func (c *ImageClient) Generate(ctx context.Context, prompt string, opts ImageOptions) ([]GeneratedImage, string, error) {
	if err := c.validateCommon(prompt, &opts); err != nil {
		return nil, "", err
	}

	payload := map[string]any{
		"model":  opts.Model,
		"prompt": prompt,
		"n":      opts.N,
	}
	addOptionalFields(payload, opts, false)

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
	images, err := c.materializeImages(ctx, parsed, opts.OutputFormat)
	if err != nil {
		return nil, "", err
	}
	return images, opts.Model, nil
}

func (c *ImageClient) Edit(ctx context.Context, prompt string, imagePaths []string, maskPath string, opts ImageOptions) ([]GeneratedImage, string, error) {
	if err := c.validateCommon(prompt, &opts); err != nil {
		return nil, "", err
	}
	if len(imagePaths) == 0 || len(imagePaths) > maxReferenceImages {
		return nil, "", fmt.Errorf("reference_images must contain between 1 and %d images", maxReferenceImages)
	}

	refs := make([]validatedFile, 0, len(imagePaths))
	for _, path := range imagePaths {
		file, err := c.validateImagePath(path, maxReferenceImageBytes, false)
		if err != nil {
			return nil, "", err
		}
		refs = append(refs, file)
	}

	var mask *validatedFile
	if strings.TrimSpace(maskPath) != "" {
		file, err := c.validateImagePath(maskPath, maxMaskBytes, true)
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
		err := writeEditMultipart(mw, prompt, refs, mask, opts)
		if closeErr := mw.Close(); err == nil {
			err = closeErr
		}
		_ = pw.CloseWithError(err)
	}()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("image edit request failed: %w", err)
	}
	defer resp.Body.Close()

	parsed, err := decodeImageResponse(resp)
	if err != nil {
		return nil, "", err
	}
	images, err := c.materializeImages(ctx, parsed, opts.OutputFormat)
	if err != nil {
		return nil, "", err
	}
	return images, opts.Model, nil
}

func (c *ImageClient) validateCommon(prompt string, opts *ImageOptions) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return errors.New("prompt is required")
	}
	if len([]rune(prompt)) > maxPromptRunes {
		return fmt.Errorf("prompt exceeds %d characters", maxPromptRunes)
	}
	if strings.TrimSpace(c.cfg.APIKey) == "" {
		return errors.New("OPENAI_API_KEY is not set")
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = c.cfg.DefaultModel
	}
	if opts.N == 0 {
		opts.N = defaultGeneratedImageCount
	}
	if opts.N < 1 || opts.N > 10 {
		return errors.New("n must be between 1 and 10")
	}
	if opts.OutputCompression != nil && (*opts.OutputCompression < 0 || *opts.OutputCompression > 100) {
		return errors.New("output_compression must be between 0 and 100")
	}
	if opts.Background == "transparent" && opts.OutputFormat != "" && opts.OutputFormat != "png" && opts.OutputFormat != "webp" {
		return errors.New("transparent background requires output_format png or webp")
	}
	return nil
}

func addOptionalFields(payload map[string]any, opts ImageOptions, editing bool) {
	if opts.Size != "" {
		payload["size"] = opts.Size
	}
	if opts.Quality != "" {
		payload["quality"] = opts.Quality
	}
	if opts.Background != "" {
		payload["background"] = opts.Background
	}
	if opts.OutputFormat != "" {
		payload["output_format"] = opts.OutputFormat
	}
	if opts.Complexity != "" {
		payload["complexity"] = opts.Complexity
	}
	if editing && opts.InputFidelity != "" {
		payload["input_fidelity"] = opts.InputFidelity
	}
	if opts.SafetyIdentifier != "" {
		payload["safety_identifier"] = opts.SafetyIdentifier
	}
	if opts.OutputCompression != nil {
		payload["output_compression"] = *opts.OutputCompression
	}
}

func writeEditMultipart(mw *multipart.Writer, prompt string, refs []validatedFile, mask *validatedFile, opts ImageOptions) error {
	fields := map[string]string{
		"model":  opts.Model,
		"prompt": prompt,
		"n":      strconv.Itoa(opts.N),
	}
	if opts.Size != "" {
		fields["size"] = opts.Size
	}
	if opts.Quality != "" {
		fields["quality"] = opts.Quality
	}
	if opts.Background != "" {
		fields["background"] = opts.Background
	}
	if opts.OutputFormat != "" {
		fields["output_format"] = opts.OutputFormat
	}
	if opts.Complexity != "" {
		fields["complexity"] = opts.Complexity
	}
	if opts.InputFidelity != "" {
		fields["input_fidelity"] = opts.InputFidelity
	}
	if opts.SafetyIdentifier != "" {
		fields["safety_identifier"] = opts.SafetyIdentifier
	}
	if opts.OutputCompression != nil {
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

func (c *ImageClient) validateImagePath(raw string, maxBytes int64, pngOnly bool) (validatedFile, error) {
	path, err := normalizeLocalPath(raw)
	if err != nil {
		return validatedFile{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return validatedFile{}, fmt.Errorf("resolve image path %q: %w", raw, err)
	}
	abs = filepath.Clean(abs)
	if err := c.checkAllowedPath(abs); err != nil {
		return validatedFile{}, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return validatedFile{}, fmt.Errorf("read image %q: %w", abs, err)
	}
	if !info.Mode().IsRegular() {
		return validatedFile{}, fmt.Errorf("image path is not a regular file: %s", abs)
	}
	if info.Size() <= 0 || info.Size() >= maxBytes {
		return validatedFile{}, fmt.Errorf("image %q must be smaller than %d MiB", abs, maxBytes>>20)
	}

	f, err := os.Open(abs)
	if err != nil {
		return validatedFile{}, fmt.Errorf("open image %q: %w", abs, err)
	}
	defer f.Close()

	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return validatedFile{}, fmt.Errorf("read image header %q: %w", abs, err)
	}
	if n == 0 {
		return validatedFile{}, fmt.Errorf("image %q is empty", abs)
	}

	mimeType := http.DetectContentType(header[:n])
	if pngOnly {
		if mimeType != "image/png" {
			return validatedFile{}, fmt.Errorf("mask is not a valid PNG: %s", abs)
		}
	} else {
		switch mimeType {
		case "image/png", "image/jpeg", "image/webp":
		default:
			return validatedFile{}, fmt.Errorf("unsupported image MIME type %q for %s; use PNG, JPEG, or WebP", mimeType, abs)
		}
	}

	name := canonicalUploadName(filepath.Base(abs), mimeType)
	return validatedFile{
		Path:     abs,
		Name:     name,
		Size:     info.Size(),
		MIMEType: mimeType,
	}, nil
}

func canonicalUploadName(name, mimeType string) string {
	ext := strings.ToLower(filepath.Ext(name))
	validExt := false
	canonicalExt := ""
	switch mimeType {
	case "image/png":
		validExt = ext == ".png"
		canonicalExt = ".png"
	case "image/jpeg":
		validExt = ext == ".jpg" || ext == ".jpeg"
		canonicalExt = ".jpg"
	case "image/webp":
		validExt = ext == ".webp"
		canonicalExt = ".webp"
	}
	if validExt || canonicalExt == "" {
		return name
	}
	if ext != "" {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if name == "" {
		name = "image"
	}
	return name + canonicalExt
}

func (c *ImageClient) checkAllowedPath(path string) error {
	if len(c.cfg.AllowedDirs) == 0 {
		return nil
	}
	for _, root := range c.cfg.AllowedDirs {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return nil
		}
	}
	return fmt.Errorf("image path %q is outside GPT_IMAGE_ALLOWED_DIRS", path)
}

func normalizeLocalPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty image path")
	}
	if !strings.HasPrefix(strings.ToLower(raw), "file://") {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid file URI %q: %w", raw, err)
	}
	path, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", fmt.Errorf("invalid file URI path %q: %w", raw, err)
	}
	if u.Host != "" && u.Host != "localhost" {
		path = "//" + u.Host + path
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
}

func (c *ImageClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if c.cfg.Organization != "" {
		req.Header.Set("OpenAI-Organization", c.cfg.Organization)
	}
	if c.cfg.Project != "" {
		req.Header.Set("OpenAI-Project", c.cfg.Project)
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
		return fmt.Errorf("image API returned HTTP %d: %s", status, envelope.Error.Message)
	}
	message := strings.TrimSpace(string(body))
	if len(message) > 1000 {
		message = message[:1000] + "…"
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return fmt.Errorf("image API returned HTTP %d: %s", status, message)
}

func (c *ImageClient) materializeImages(ctx context.Context, response ImageResponse, requestedFormat string) ([]GeneratedImage, error) {
	format := strings.ToLower(strings.TrimSpace(response.OutputFormat))
	if format == "" {
		format = strings.ToLower(strings.TrimSpace(requestedFormat))
	}
	if format == "" {
		format = "png"
	}

	result := make([]GeneratedImage, 0, len(response.Data))
	for i, item := range response.Data {
		var imageBytes []byte
		var err error
		switch {
		case item.B64JSON != "":
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

		mimeType, ext := outputType(format, imageBytes)
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

func outputType(format string, data []byte) (mimeType, ext string) {
	switch strings.ToLower(format) {
	case "png":
		return "image/png", ".png"
	case "jpeg", "jpg":
		return "image/jpeg", ".jpg"
	case "webp":
		return "image/webp", ".webp"
	case "gif":
		return "image/gif", ".gif"
	case "svg":
		return "image/svg+xml", ".svg"
	}
	mimeType = http.DetectContentType(data)
	switch mimeType {
	case "image/png":
		return mimeType, ".png"
	case "image/jpeg":
		return mimeType, ".jpg"
	case "image/webp":
		return mimeType, ".webp"
	case "image/gif":
		return mimeType, ".gif"
	default:
		return mimeType, ".bin"
	}
}
