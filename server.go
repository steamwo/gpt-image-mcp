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
	Prompt            string `json:"prompt" jsonschema:"Text description of the image to generate."`
	Model             string `json:"model,omitempty" jsonschema:"Optional GPT Image model override. Defaults to GPT_IMAGE_MODEL."`
	Size              string `json:"size,omitempty" jsonschema:"Optional output size, for example 1024x1024, 1536x1024, 1024x1536, auto, or a model-supported WIDTHxHEIGHT."`
	Quality           string `json:"quality,omitempty" jsonschema:"Optional quality such as auto, low, medium, high, xhigh, or max when supported by the model."`
	Background        string `json:"background,omitempty" jsonschema:"Optional background: auto, opaque, or transparent."`
	OutputFormat      string `json:"output_format,omitempty" jsonschema:"Optional output format such as png, jpeg, webp, gif, or svg when supported."`
	Complexity        string `json:"complexity,omitempty" jsonschema:"Optional planning complexity: low, medium, or high on supported GPT Image models."`
	N                 int    `json:"n,omitempty" jsonschema:"Number of images to generate, from 1 to 10. Defaults to 1."`
	OutputCompression *int   `json:"output_compression,omitempty" jsonschema:"Optional 0-100 compression level for JPEG or WebP outputs."`
	SafetyIdentifier  string `json:"safety_identifier,omitempty" jsonschema:"Optional stable privacy-preserving end-user identifier for safety monitoring."`
}

type EditImageInput struct {
	Prompt            string   `json:"prompt" jsonschema:"Describe the desired result and what should change or stay unchanged."`
	ReferenceImages   []string `json:"reference_images" jsonschema:"Absolute local file paths or file:// URIs for 1-16 user-provided PNG, JPEG, or WebP reference images. When the user attached images in WorkBuddy, use their local attachment paths here."`
	Mask              string   `json:"mask,omitempty" jsonschema:"Optional local PNG mask path. Transparent pixels indicate where the first reference image may be edited."`
	Model             string   `json:"model,omitempty" jsonschema:"Optional GPT Image model override. Defaults to GPT_IMAGE_MODEL."`
	Size              string   `json:"size,omitempty" jsonschema:"Optional output size, for example 1024x1024, 1536x1024, 1024x1536, auto, or a model-supported WIDTHxHEIGHT."`
	Quality           string   `json:"quality,omitempty" jsonschema:"Optional quality such as auto, low, medium, high, xhigh, or max when supported by the model."`
	Background        string   `json:"background,omitempty" jsonschema:"Optional background: auto, opaque, or transparent."`
	OutputFormat      string   `json:"output_format,omitempty" jsonschema:"Optional output format such as png, jpeg, webp, gif, or svg when supported."`
	Complexity        string   `json:"complexity,omitempty" jsonschema:"Optional planning complexity: low, medium, or high on supported GPT Image models."`
	InputFidelity     string   `json:"input_fidelity,omitempty" jsonschema:"Optional input fidelity for older models that expose it. Omit for GPT-Image-2 and newer models unless your provider documents support."`
	N                 int      `json:"n,omitempty" jsonschema:"Number of images to generate, from 1 to 10. Defaults to 1."`
	OutputCompression *int     `json:"output_compression,omitempty" jsonschema:"Optional 0-100 compression level for JPEG or WebP outputs."`
	SafetyIdentifier  string   `json:"safety_identifier,omitempty" jsonschema:"Optional stable privacy-preserving end-user identifier for safety monitoring."`
}

type ImageToolOutput struct {
	Files          []string `json:"files"`
	Model          string   `json:"model"`
	Count          int      `json:"count"`
	RevisedPrompts []string `json:"revised_prompts,omitempty"`
	Embedded       int      `json:"embedded_previews"`
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
		Description: "Generate one or more images from text using the configured GPT Image API. Use edit_image instead when the user supplies reference images.",
	}, s.generateImage)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "edit_image",
		Description: "Generate or edit images using 1-16 local reference images. Use this when the user attaches a reference image or asks to preserve visual details from existing images. Reference paths are read locally and sent to the configured /images/edits endpoint.",
	}, s.editImage)

	return s
}

func (s *MCPServer) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

func (s *MCPServer) generateImage(ctx context.Context, _ *mcp.CallToolRequest, input GenerateImageInput) (*mcp.CallToolResult, any, error) {
	images, model, err := s.client.Generate(ctx, input.Prompt, imageOptionsFromGenerate(input))
	if err != nil {
		return toolError(err), nil, nil
	}
	return s.successResult(images, model)
}

func (s *MCPServer) editImage(ctx context.Context, _ *mcp.CallToolRequest, input EditImageInput) (*mcp.CallToolResult, any, error) {
	images, model, err := s.client.Edit(ctx, input.Prompt, input.ReferenceImages, input.Mask, imageOptionsFromEdit(input))
	if err != nil {
		return toolError(err), nil, nil
	}
	return s.successResult(images, model)
}

func imageOptionsFromGenerate(input GenerateImageInput) ImageOptions {
	return ImageOptions{
		Model:             input.Model,
		Size:              input.Size,
		Quality:           input.Quality,
		Background:        input.Background,
		OutputFormat:      input.OutputFormat,
		Complexity:        input.Complexity,
		SafetyIdentifier:  input.SafetyIdentifier,
		N:                 input.N,
		OutputCompression: input.OutputCompression,
	}
}

func imageOptionsFromEdit(input EditImageInput) ImageOptions {
	return ImageOptions{
		Model:             input.Model,
		Size:              input.Size,
		Quality:           input.Quality,
		Background:        input.Background,
		OutputFormat:      input.OutputFormat,
		Complexity:        input.Complexity,
		InputFidelity:     input.InputFidelity,
		SafetyIdentifier:  input.SafetyIdentifier,
		N:                 input.N,
		OutputCompression: input.OutputCompression,
	}
}

func (s *MCPServer) successResult(images []GeneratedImage, model string) (*mcp.CallToolResult, any, error) {
	if err := os.MkdirAll(s.cfg.OutputDir, 0o755); err != nil {
		return toolError(fmt.Errorf("create output directory: %w", err)), nil, nil
	}

	output := ImageToolOutput{Model: model, Count: len(images)}
	content := make([]mcp.Content, 0, len(images)+1)
	var embeddedBytes int64

	for _, image := range images {
		file, err := os.CreateTemp(s.cfg.OutputDir, "gpt-image-*"+image.Extension)
		if err != nil {
			return toolError(fmt.Errorf("create output file: %w", err)), nil, nil
		}
		path := file.Name()
		if _, err := file.Write(image.Bytes); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return toolError(fmt.Errorf("write output file: %w", err)), nil, nil
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return toolError(fmt.Errorf("close output file: %w", err)), nil, nil
		}
		abs, _ := filepath.Abs(path)
		output.Files = append(output.Files, abs)
		if strings.TrimSpace(image.RevisedPrompt) != "" {
			output.RevisedPrompts = append(output.RevisedPrompts, image.RevisedPrompt)
		}

		if s.cfg.EmbedMaxBytes > 0 && embeddedBytes+int64(len(image.Bytes)) <= s.cfg.EmbedMaxBytes {
			content = append(content, &mcp.ImageContent{Data: image.Bytes, MIMEType: image.MIMEType})
			embeddedBytes += int64(len(image.Bytes))
			output.Embedded++
		}
	}

	summary := fmt.Sprintf("Generated %d image(s) with %s. Saved locally:\n- %s", len(output.Files), model, strings.Join(output.Files, "\n- "))
	if output.Embedded < len(output.Files) {
		summary += fmt.Sprintf("\nEmbedded %d/%d preview(s); remaining images are available at the saved paths.", output.Embedded, len(output.Files))
	}
	content = append([]mcp.Content{&mcp.TextContent{Text: summary}}, content...)

	return &mcp.CallToolResult{Content: content}, output, nil
}

func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		},
	}
}
