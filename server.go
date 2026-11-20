package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPServer struct {
	cfg    Config
	client *ImageClient
	server *mcp.Server
}

type GenerateImageInput struct {
	Prompt      string `json:"prompt" jsonschema:"Describe the image to create. Usually this is the only required argument."`
	Model       string `json:"model,omitempty" jsonschema:"Optional model override. Omit unless the user explicitly requested a model."`
	Size        string `json:"size,omitempty" jsonschema:"Optional output size such as 1024x1024 or 1536x1024. Omit to let the image provider choose."`
	Quality     string `json:"quality,omitempty" jsonschema:"Optional quality such as low, medium, high, or auto. Omit unless quality matters to the request."`
	N           int    `json:"n,omitempty" jsonschema:"Number of images to generate, 1-10. Defaults to 1."`
	Transparent bool   `json:"transparent,omitempty" jsonschema:"Set true only when the user explicitly needs a transparent background."`
}

type EditImageInput struct {
	Prompt          string   `json:"prompt" jsonschema:"Describe the desired result, including what should change and what must stay unchanged."`
	ReferenceImages []string `json:"reference_images" jsonschema:"One to sixteen image sources. Each source may be a local path, file:// URI, public HTTP(S) URL, or base64 data URI containing PNG, JPEG, or WebP."`
	Mask            string   `json:"mask,omitempty" jsonschema:"Optional PNG mask source. It accepts the same source forms as reference_images. Transparent pixels indicate editable areas."`
	Model           string   `json:"model,omitempty" jsonschema:"Optional model override. Omit unless the user explicitly requested a model."`
	Size            string   `json:"size,omitempty" jsonschema:"Optional output size such as 1024x1024 or 1536x1024. Omit to let the image provider choose."`
	Quality         string   `json:"quality,omitempty" jsonschema:"Optional quality such as low, medium, high, or auto. Omit unless quality matters to the request."`
	N               int      `json:"n,omitempty" jsonschema:"Number of images to generate, 1-10. Defaults to 1."`
	Transparent     bool     `json:"transparent,omitempty" jsonschema:"Set true only when the user explicitly needs a transparent background."`
}

type ImageArtifact struct {
	Path          string `json:"path"`
	MIMEType      string `json:"mime_type"`
	SizeBytes     int64  `json:"size_bytes"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
	Embedded      bool   `json:"embedded_preview"`
}

type ImageToolOutput struct {
	Images          []ImageArtifact  `json:"images,omitempty"`
	Model           string           `json:"model,omitempty"`
	Count           int              `json:"count"`
	ProviderProfile string           `json:"provider_profile"`
	Error           *ToolErrorDetail `json:"error,omitempty"`
}

func newMCPServer(cfg Config, client *ImageClient) *MCPServer {
	s := &MCPServer{
		cfg:    cfg,
		client: client,
		server: mcp.NewServer(&mcp.Implementation{
			Name:    "gpt-image-mcp",
			Version: version,
		}, nil),
	}

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "generate_image",
		Description: "Generate images from text. Prefer calling with only prompt; use optional size, quality, model, n, or transparent only when the user request requires them. Use edit_image when reference images are provided.",
	}, s.generateImage)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "edit_image",
		Description: "Generate or edit images from 1-16 references. Reference images may be local paths, file URIs, public HTTP(S) URLs, or base64 data URIs. Prefer only prompt plus reference_images unless the user explicitly needs other controls.",
	}, s.editImage)

	return s
}

func (s *MCPServer) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

func (s *MCPServer) generateImage(ctx context.Context, _ *mcp.CallToolRequest, input GenerateImageInput) (*mcp.CallToolResult, ImageToolOutput, error) {
	opts := imageOptionsFromGenerate(input)
	images, model, err := s.client.Generate(ctx, input.Prompt, opts)
	if err != nil {
		return s.errorResult(err, s.effectiveModel(input.Model))
	}
	return s.successResult(images, model)
}

func (s *MCPServer) editImage(ctx context.Context, _ *mcp.CallToolRequest, input EditImageInput) (*mcp.CallToolResult, ImageToolOutput, error) {
	opts := imageOptionsFromEdit(input)
	images, model, err := s.client.Edit(ctx, input.Prompt, input.ReferenceImages, input.Mask, opts)
	if err != nil {
		return s.errorResult(err, s.effectiveModel(input.Model))
	}
	return s.successResult(images, model)
}

func imageOptionsFromGenerate(input GenerateImageInput) ImageOptions {
	return ImageOptions{
		Model:       input.Model,
		Size:        input.Size,
		Quality:     input.Quality,
		N:           input.N,
		Transparent: input.Transparent,
	}
}

func imageOptionsFromEdit(input EditImageInput) ImageOptions {
	return ImageOptions{
		Model:       input.Model,
		Size:        input.Size,
		Quality:     input.Quality,
		N:           input.N,
		Transparent: input.Transparent,
	}
}

func (s *MCPServer) successResult(images []GeneratedImage, model string) (*mcp.CallToolResult, ImageToolOutput, error) {
	if err := os.MkdirAll(s.cfg.OutputDir, 0o755); err != nil {
		return s.errorResult(fmt.Errorf("create output directory: %w", err), model)
	}

	output := ImageToolOutput{
		Model:           model,
		Count:           len(images),
		ProviderProfile: s.client.capabilities.Name,
	}
	content := make([]mcp.Content, 0, len(images)+1)
	var embeddedBytes int64
	paths := make([]string, 0, len(images))

	for _, image := range images {
		file, err := os.CreateTemp(s.cfg.OutputDir, "gpt-image-*"+image.Extension)
		if err != nil {
			return s.errorResult(fmt.Errorf("create output file: %w", err), model)
		}
		path := file.Name()
		if _, err := file.Write(image.Bytes); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return s.errorResult(fmt.Errorf("write output file: %w", err), model)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return s.errorResult(fmt.Errorf("close output file: %w", err), model)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		artifact := ImageArtifact{
			Path:          abs,
			MIMEType:      image.MIMEType,
			SizeBytes:     int64(len(image.Bytes)),
			RevisedPrompt: image.RevisedPrompt,
		}
		paths = append(paths, abs)

		if s.cfg.EmbedMaxBytes > 0 && embeddedBytes+int64(len(image.Bytes)) <= s.cfg.EmbedMaxBytes {
			content = append(content, &mcp.ImageContent{Data: image.Bytes, MIMEType: image.MIMEType})
			embeddedBytes += int64(len(image.Bytes))
			artifact.Embedded = true
		}
		output.Images = append(output.Images, artifact)
	}

	summary := fmt.Sprintf("Generated %d image(s) with %s using %s profile. Saved locally:\n- %s", len(output.Images), model, output.ProviderProfile, strings.Join(paths, "\n- "))
	embeddedCount := 0
	for _, artifact := range output.Images {
		if artifact.Embedded {
			embeddedCount++
		}
	}
	if embeddedCount < len(output.Images) {
		summary += fmt.Sprintf("\nEmbedded %d/%d preview(s); all files are available at the saved paths.", embeddedCount, len(output.Images))
	}
	content = append([]mcp.Content{&mcp.TextContent{Text: summary}}, content...)

	return &mcp.CallToolResult{Content: content}, output, nil
}

func (s *MCPServer) errorResult(err error, model string) (*mcp.CallToolResult, ImageToolOutput, error) {
	detail := classifyToolError(err)
	output := ImageToolOutput{
		Model:           model,
		ProviderProfile: s.client.capabilities.Name,
		Error:           &detail,
	}
	text := detail.Message
	if detail.Hint != "" {
		text += " " + detail.Hint
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}, output, nil
}

func (s *MCPServer) effectiveModel(requested string) string {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested)
	}
	return s.cfg.DefaultModel
}
