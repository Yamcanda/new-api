package gemini

import (
	"encoding/json"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/relay/helper"
	"one-api/service"

	"github.com/gin-gonic/gin"
)

func GeminiTextGenerationHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode) {
	// 读取响应体
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, service.OpenAIErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	err = resp.Body.Close()
	if err != nil {
		return nil, service.OpenAIErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError)
	}

	if common.DebugEnabled {
		println(string(responseBody))
	}

	// 解析为 Gemini 原生响应格式
	var geminiResponse GeminiChatResponse
	err = common.DecodeJson(responseBody, &geminiResponse)
	if err != nil {
		return nil, service.OpenAIErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError)
	}

	// 计算使用量（基于 UsageMetadata）
	usage := dto.Usage{
		PromptTokens:     geminiResponse.UsageMetadata.PromptTokenCount,
		CompletionTokens: geminiResponse.UsageMetadata.CandidatesTokenCount + geminiResponse.UsageMetadata.ThoughtsTokenCount,
		TotalTokens:      geminiResponse.UsageMetadata.TotalTokenCount,
	}

	usage.CompletionTokenDetails.ReasoningTokens = geminiResponse.UsageMetadata.ThoughtsTokenCount

	for _, detail := range geminiResponse.UsageMetadata.PromptTokensDetails {
		if detail.Modality == "AUDIO" {
			usage.PromptTokensDetails.AudioTokens = detail.TokenCount
		} else if detail.Modality == "TEXT" {
			usage.PromptTokensDetails.TextTokens = detail.TokenCount
		}
	}

	// 直接返回 Gemini 原生格式的 JSON 响应
	jsonResponse, err := json.Marshal(geminiResponse)
	if err != nil {
		return nil, service.OpenAIErrorWrapper(err, "marshal_response_body_failed", http.StatusInternalServerError)
	}

	// 设置响应头并写入响应
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	if err != nil {
		return nil, service.OpenAIErrorWrapper(err, "write_response_failed", http.StatusInternalServerError)
	}

	return &usage, nil
}

func GeminiTextGenerationStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode) {
	var usage = &dto.Usage{}
	var imageCount int

	helper.SetEventStreamHeaders(c)

	helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		var geminiResponse GeminiChatResponse
		err := common.DecodeJsonStr(data, &geminiResponse)
		if err != nil {
			common.LogError(c, "error unmarshalling stream response: "+err.Error())
			return false
		}

		// 统计图片数量
		for _, candidate := range geminiResponse.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil && part.InlineData.MimeType != "" {
					imageCount++
				}
			}
		}

		// 更新使用量统计
		if geminiResponse.UsageMetadata.TotalTokenCount != 0 {
			usage.PromptTokens = geminiResponse.UsageMetadata.PromptTokenCount
			usage.CompletionTokens = geminiResponse.UsageMetadata.CandidatesTokenCount + geminiResponse.UsageMetadata.ThoughtsTokenCount
			usage.TotalTokens = geminiResponse.UsageMetadata.TotalTokenCount
			usage.CompletionTokenDetails.ReasoningTokens = geminiResponse.UsageMetadata.ThoughtsTokenCount
			for _, detail := range geminiResponse.UsageMetadata.PromptTokensDetails {
				if detail.Modality == "AUDIO" {
					usage.PromptTokensDetails.AudioTokens = detail.TokenCount
				} else if detail.Modality == "TEXT" {
					usage.PromptTokensDetails.TextTokens = detail.TokenCount
				}
			}
		}

		// 直接发送 GeminiChatResponse 响应
		err = helper.ObjectData(c, geminiResponse)
		if err != nil {
			common.LogError(c, err.Error())
		}

		return true
	})

	if imageCount != 0 {
		if usage.CompletionTokens == 0 {
			usage.CompletionTokens = imageCount * 258
		}
	}

	// 计算最终使用量
	// usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens

	// 移除流式响应结尾的[Done]，因为Gemini API没有发送Done的行为
	//helper.Done(c)

	return usage, nil
}
