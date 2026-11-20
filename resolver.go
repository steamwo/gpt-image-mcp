package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func newReferenceHTTPClient(cfg Config) *http.Client {
	transport := &http.Transport{}
	if !cfg.AllowPrivateRemoteImages {
		transport.Proxy = nil
		transport.DialContext = safeDialContext
	}
	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects while downloading reference image")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("reference image redirect uses unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, candidate := range ips {
		if isPublicIP(candidate.IP) {
			dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		}
	}
	return nil, fmt.Errorf("remote image host %q resolves only to a private or local network address", host)
}

func isPublicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func (c *ImageClient) resolveImageSource(ctx context.Context, raw string, maxBytes int64, pngOnly bool, tempDir string) (validatedFile, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: "image source cannot be empty"}
	}

	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "data:"):
		return c.resolveDataURI(raw, maxBytes, pngOnly, tempDir)
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		if !c.cfg.AllowRemoteImages {
			return validatedFile{}, &InputError{Parameter: "reference_images", Message: "HTTP(S) reference images are disabled by GPT_IMAGE_ALLOW_REMOTE_IMAGES"}
		}
		return c.resolveRemoteImage(ctx, raw, maxBytes, pngOnly, tempDir)
	default:
		return c.validateImagePath(raw, maxBytes, pngOnly)
	}
}

func (c *ImageClient) resolveDataURI(raw string, maxBytes int64, pngOnly bool, tempDir string) (validatedFile, error) {
	header, encoded, ok := strings.Cut(raw, ",")
	if !ok || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: "data URI must use base64 encoding"}
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")))
	if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/webp" {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: "data URI must contain PNG, JPEG, or WebP image data"}
	}
	if int64(base64.StdEncoding.DecodedLen(len(encoded))) > maxBytes {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: fmt.Sprintf("data URI image must be smaller than %d MiB", maxBytes>>20)}
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: "invalid base64 image data"}
	}
	path, err := writeTemporarySource(tempDir, "data-image-*", data)
	if err != nil {
		return validatedFile{}, err
	}
	return c.validateImageFile(path, "image", maxBytes, pngOnly, false)
}

func (c *ImageClient) resolveRemoteImage(ctx context.Context, raw string, maxBytes int64, pngOnly bool, tempDir string) (validatedFile, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: "invalid HTTP(S) image URL"}
	}
	if u.User != nil {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: "image URLs with embedded credentials are not allowed"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return validatedFile{}, err
	}
	resp, err := c.referenceHTTPClient.Do(req)
	if err != nil {
		return validatedFile{}, fmt.Errorf("download reference image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return validatedFile{}, fmt.Errorf("download reference image: HTTP %d", resp.StatusCode)
	}

	file, err := os.CreateTemp(tempDir, "remote-image-*")
	if err != nil {
		return validatedFile{}, fmt.Errorf("create temporary reference image: %w", err)
	}
	path := file.Name()
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return validatedFile{}, fmt.Errorf("download reference image: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return validatedFile{}, fmt.Errorf("close temporary reference image: %w", closeErr)
	}
	if written > maxBytes {
		_ = os.Remove(path)
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: fmt.Sprintf("remote image must be smaller than %d MiB", maxBytes>>20)}
	}

	name := filepath.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		name = "image"
	}
	return c.validateImageFile(path, name, maxBytes, pngOnly, false)
}

func writeTemporarySource(dir, pattern string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
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
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return validatedFile{}, fmt.Errorf("read image %q: %w", abs, err)
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return validatedFile{}, fmt.Errorf("resolve image path %q: %w", raw, err)
	}
	if err := c.checkAllowedPath(realPath); err != nil {
		return validatedFile{}, err
	}
	return c.validateImageFile(realPath, filepath.Base(abs), maxBytes, pngOnly, false)
}

func (c *ImageClient) validateImageFile(path, uploadName string, maxBytes int64, pngOnly bool, enforceAllowed bool) (validatedFile, error) {
	if enforceAllowed {
		if err := c.checkAllowedPath(path); err != nil {
			return validatedFile{}, err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return validatedFile{}, fmt.Errorf("read image %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return validatedFile{}, fmt.Errorf("image path is not a regular file: %s", path)
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: fmt.Sprintf("image must be no larger than %d MiB", maxBytes>>20)}
	}

	f, err := os.Open(path)
	if err != nil {
		return validatedFile{}, fmt.Errorf("open image %q: %w", path, err)
	}
	defer f.Close()
	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return validatedFile{}, fmt.Errorf("read image header %q: %w", path, err)
	}
	if n == 0 {
		return validatedFile{}, &InputError{Parameter: "reference_images", Message: "image is empty"}
	}

	mimeType := http.DetectContentType(header[:n])
	if pngOnly {
		if mimeType != "image/png" {
			return validatedFile{}, &InputError{Parameter: "mask", Message: "mask must be a valid PNG image"}
		}
	} else {
		switch mimeType {
		case "image/png", "image/jpeg", "image/webp":
		default:
			return validatedFile{}, &InputError{Parameter: "reference_images", Message: fmt.Sprintf("unsupported image MIME type %q; use PNG, JPEG, or WebP", mimeType)}
		}
	}

	return validatedFile{
		Path:     path,
		Name:     canonicalUploadName(uploadName, mimeType),
		Size:     info.Size(),
		MIMEType: mimeType,
	}, nil
}

func (c *ImageClient) checkAllowedPath(path string) error {
	if len(c.cfg.AllowedDirs) == 0 {
		return nil
	}
	path = canonicalFilesystemPath(path)
	for _, root := range c.cfg.AllowedDirs {
		root = canonicalFilesystemPath(root)
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return nil
		}
	}
	return fmt.Errorf("image path %q is outside GPT_IMAGE_ALLOWED_DIRS", path)
}

func canonicalFilesystemPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func normalizeLocalPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", &InputError{Parameter: "reference_images", Message: "empty image path"}
	}
	if !strings.HasPrefix(strings.ToLower(raw), "file://") {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", &InputError{Parameter: "reference_images", Message: "invalid file URI"}
	}
	path, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", &InputError{Parameter: "reference_images", Message: "invalid file URI path"}
	}
	if u.Host != "" && u.Host != "localhost" {
		path = "//" + u.Host + path
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
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
