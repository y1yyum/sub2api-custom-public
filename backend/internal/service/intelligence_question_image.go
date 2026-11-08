package service

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
)

const intelligenceImagePrefix = "data:image/png;base64,"

func validateIntelligencePromptImage(dataURL string) error {
	if !strings.HasPrefix(dataURL, intelligenceImagePrefix) {
		return invalidIntelligenceQuestion("prompt_image must be a PNG data URL; save the question again to generate its image")
	}
	if len(dataURL) > 2<<20 {
		return invalidIntelligenceQuestion("prompt_image must be at most 2 MiB")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, intelligenceImagePrefix))
	if err != nil {
		return invalidIntelligenceQuestion("prompt_image contains invalid base64")
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return invalidIntelligenceQuestion("prompt_image must contain a valid PNG")
	}
	if config.Width > 2048 || config.Height > 16384 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return invalidIntelligenceQuestion("prompt_image dimensions are too large")
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		return invalidIntelligenceQuestion("prompt_image must contain a complete PNG")
	}
	return nil
}
