package service

import "strings"

const accountTestQuestionImageKey = "account_test_question_image"

func accountTestImageContent(dataURL string) []map[string]any {
	return []map[string]any{{
		"type": "image",
		"source": map[string]any{
			"type": "base64", "media_type": "image/png",
			"data": strings.TrimPrefix(dataURL, intelligenceImagePrefix),
		},
	}}
}

func accountTestGeminiParts(prompt, dataURL string) []map[string]any {
	if dataURL != "" {
		return []map[string]any{{"inlineData": map[string]any{
			"mimeType": "image/png", "data": strings.TrimPrefix(dataURL, intelligenceImagePrefix),
		}}}
	}
	return []map[string]any{{"text": prompt}}
}
