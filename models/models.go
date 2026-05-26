package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agr/config"
)

// ModelsResponse /v1/models 接口返回结构
type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo 模型元数据
type ModelInfo struct {
	Slug                          string                  `json:"slug"`
	DisplayName                   string                  `json:"display_name"`
	Description                   *string                 `json:"description,omitempty"`
	DefaultReasoningLevel         *ReasoningEffort        `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels      []ReasoningEffortPreset `json:"supported_reasoning_levels"`
	ShellType                     ConfigShellToolType     `json:"shell_type"`
	Visibility                    ModelVisibility         `json:"visibility"`
	SupportedInAPI                bool                    `json:"supported_in_api"`
	Priority                      int32                   `json:"priority"`
	AdditionalSpeedTiers          []string                `json:"additional_speed_tiers,omitempty"`
	ServiceTiers                  []ModelServiceTier      `json:"service_tiers,omitempty"`
	DefaultServiceTier            *string                 `json:"default_service_tier,omitempty"`
	AvailabilityNux               *ModelAvailabilityNux   `json:"availability_nux,omitempty"`
	Upgrade                       *ModelInfoUpgrade       `json:"upgrade,omitempty"`
	BaseInstructions              string                  `json:"base_instructions"`
	ModelMessages                 *ModelMessages          `json:"model_messages,omitempty"`
	SupportsReasoningSummaries    *bool                   `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary       ReasoningSummary        `json:"default_reasoning_summary"`
	SupportVerbosity              bool                    `json:"support_verbosity"`
	DefaultVerbosity              *Verbosity              `json:"default_verbosity,omitempty"`
	ApplyPatchToolType            *ApplyPatchToolType     `json:"apply_patch_tool_type,omitempty"`
	WebSearchToolType             WebSearchToolType       `json:"web_search_tool_type"`
	TruncationPolicy              TruncationPolicyConfig  `json:"truncation_policy"`
	SupportsParallelToolCalls     *bool                   `json:"supports_parallel_tool_calls"`
	SupportsImageDetailOriginal   bool                    `json:"supports_image_detail_original,omitempty"`
	ContextWindow                 *int64                  `json:"context_window,omitempty"`
	MaxContextWindow              *int64                  `json:"max_context_window,omitempty"`
	AutoCompactTokenLimit         *int64                  `json:"auto_compact_token_limit,omitempty"`
	EffectiveContextWindowPercent int64                   `json:"effective_context_window_percent"`
	ExperimentalSupportedTools    []string                `json:"experimental_supported_tools"`
	InputModalities               []InputModality         `json:"input_modalities"`
	SupportsSearchTool            bool                    `json:"supports_search_tool,omitempty"`
}

// ReasoningEffort 推理努力级别
type ReasoningEffort string

const (
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortMinimal ReasoningEffort = "minimal"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortXHigh   ReasoningEffort = "xhigh"
)

// ReasoningEffortPreset 推理努力选项
type ReasoningEffortPreset struct {
	Effort      ReasoningEffort `json:"effort"`
	Description string          `json:"description"`
}

// InputModality 输入模态
type InputModality string

const (
	InputModalityText  InputModality = "text"
	InputModalityImage InputModality = "image"
)

// ConfigShellToolType Shell 执行类型
type ConfigShellToolType string

const (
	ConfigShellToolTypeDefault      ConfigShellToolType = "default"
	ConfigShellToolTypeLocal        ConfigShellToolType = "local"
	ConfigShellToolTypeUnifiedExec  ConfigShellToolType = "unified_exec"
	ConfigShellToolTypeDisabled     ConfigShellToolType = "disabled"
	ConfigShellToolTypeShellCommand ConfigShellToolType = "shell_command"
)

// ModelVisibility 模型可见性
type ModelVisibility string

const (
	ModelVisibilityList ModelVisibility = "list"
	ModelVisibilityHide ModelVisibility = "hide"
	ModelVisibilityNone ModelVisibility = "none"
)

// ModelServiceTier 服务层级
type ModelServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ModelAvailabilityNux 可用性提示
type ModelAvailabilityNux struct {
	Message string `json:"message"`
}

// ModelInfoUpgrade 模型升级信息
type ModelInfoUpgrade struct {
	Model             string `json:"model"`
	MigrationMarkdown string `json:"migration_markdown"`
}

// ModelMessages 模型消息模板
type ModelMessages struct {
	InstructionsTemplate  *string                     `json:"instructions_template,omitempty"`
	InstructionsVariables *ModelInstructionsVariables `json:"instructions_variables,omitempty"`
}

// ModelInstructionsVariables 模型指令变量
type ModelInstructionsVariables struct {
	PersonalityDefault   *string `json:"personality_default,omitempty"`
	PersonalityFriendly  *string `json:"personality_friendly,omitempty"`
	PersonalityPragmatic *string `json:"personality_pragmatic,omitempty"`
}

// ReasoningSummary 推理摘要类型
type ReasoningSummary string

const (
	ReasoningSummaryAuto     ReasoningSummary = "auto"
	ReasoningSummaryConcise  ReasoningSummary = "concise"
	ReasoningSummaryDetailed ReasoningSummary = "detailed"
	ReasoningSummaryNone     ReasoningSummary = "none"
)

// Verbosity 输出详细程度
type Verbosity string

const (
	VerbosityLow    Verbosity = "low"
	VerbosityMedium Verbosity = "medium"
	VerbosityHigh   Verbosity = "high"
)

// ApplyPatchToolType 应用补丁工具类型
type ApplyPatchToolType string

const (
	ApplyPatchToolTypeFreeform ApplyPatchToolType = "freeform"
)

// WebSearchToolType 网页搜索工具类型
type WebSearchToolType string

const (
	WebSearchToolTypeText         WebSearchToolType = "text"
	WebSearchToolTypeTextAndImage WebSearchToolType = "text_and_image"
)

// TruncationPolicyConfig 截断策略配置
type TruncationPolicyConfig struct {
	Mode  TruncationMode `json:"mode"`
	Limit int64          `json:"limit"`
}

// TruncationMode 截断模式
type TruncationMode string

const (
	TruncationModeBytes  TruncationMode = "bytes"
	TruncationModeTokens TruncationMode = "tokens"
)

// LoadModels 根据配置加载模型信息
// 如果顶层配置了 models_config，从 ~/.agr/<filename> 加载 JSON
// 否则根据 router 中配置的模型名生成默认的 ModelInfo
func LoadModels(cfg *config.Config) (*ModelsResponse, error) {
	// 从顶层 server.models_config 加载
	if cfg.Server.ModelsConfig != "" {
		models, err := loadModelsFromFile(cfg.Server.ModelsConfig)
		if err != nil {
			return nil, fmt.Errorf("加载 models_config 失败: %w", err)
		}
		return &ModelsResponse{Models: models}, nil
	}

	// 没有配置 models_config，根据 router 中的模型名生成默认 ModelInfo
	allModels := generateDefaultModels(cfg)
	return &ModelsResponse{Models: allModels}, nil
}

// loadModelsFromFile 从 ~/.agr/<filename> 加载模型配置 JSON
func loadModelsFromFile(filename string) ([]ModelInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("获取用户目录失败: %w", err)
	}

	filePath := filepath.Join(home, ".agr", filename)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取模型配置文件 %s 失败: %w", filePath, err)
	}

	var models []ModelInfo
	var resp ModelsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		// 尝试直接解析为数组
		if err2 := json.Unmarshal(data, &models); err2 != nil {
			return nil, fmt.Errorf("解析模型配置 JSON 失败: %w", err)
		}
	} else {
		models = resp.Models
	}

	// 填充缺失字段的默认值
	for i := range models {
		applyDefaults(&models[i])
	}

	return models, nil
}

// applyDefaults 为 ModelInfo 中未设置的字段填充合理默认值
// 使用 buildDefaultModelInfo 作为默认值来源，通过 JSON map merge 类似 JS Object.assign({}, defaults, m)
func applyDefaults(m *ModelInfo) {
	defaults := buildDefaultModelInfo(m.Slug)

	defaultBytes, _ := json.Marshal(defaults)
	modelBytes, _ := json.Marshal(m)

	var defaultMap map[string]json.RawMessage
	var modelMap map[string]json.RawMessage
	json.Unmarshal(defaultBytes, &defaultMap)
	json.Unmarshal(modelBytes, &modelMap)

	result := make(map[string]json.RawMessage, len(defaultMap))
	// 以 defaults 为基础
	for k, v := range defaultMap {
		result[k] = v
	}
	// 用 m 的非零值覆盖 defaults
	for k, v := range modelMap {
		if dVal, ok := defaultMap[k]; ok {
			result[k] = mergeJSONValue(v, dVal)
		} else {
			result[k] = v
		}
	}

	merged, _ := json.Marshal(result)
	json.Unmarshal(merged, m)
}

// mergeJSONValue 递归合并两个 JSON 值，类似 Object.assign：
//   - 基本类型：model 非零则保留，否则使用 default
//   - 对象类型：递归合并每个字段
//   - 数组类型：model 非 null 则保留，否则使用 default
func mergeJSONValue(model, defaults json.RawMessage) json.RawMessage {
	modelStr := strings.TrimSpace(string(model))

	// model 为 null，使用 default
	if modelStr == "null" {
		return defaults
	}

	// 尝试作为对象递归合并
	var mMap, dMap map[string]json.RawMessage
	if json.Unmarshal(model, &mMap) == nil && json.Unmarshal(defaults, &dMap) == nil {
		result := make(map[string]json.RawMessage, len(dMap))
		for k, v := range dMap {
			result[k] = v
		}
		for k, v := range mMap {
			if dVal, ok := dMap[k]; ok {
				result[k] = mergeJSONValue(v, dVal)
			} else {
				result[k] = v
			}
		}
		merged, _ := json.Marshal(result)
		return merged
	}

	// 基本类型：零值则使用 default
	if isZeroJSON(modelStr) {
		return defaults
	}
	return model
}

// isZeroJSON 判断 JSON 值是否为"零值"（应被 default 覆盖）
// null、空字符串、0 数值视为零值；false 不视为零值（保留用户显式设置）
func isZeroJSON(s string) bool {
	return s == "null" || s == `""` || s == "0" || s == "0.0"
}

// generateDefaultModels 根据 router 配置生成默认的模型列表
func generateDefaultModels(cfg *config.Config) []ModelInfo {
	seen := make(map[string]bool)
	var models []ModelInfo

	for clientModel := range cfg.Router {
		if clientModel == "default" {
			continue
		}
		if seen[clientModel] {
			continue
		}
		seen[clientModel] = true
		models = append(models, buildDefaultModelInfo(clientModel))
	}

	// 也为 default 路由生成一个条目（使用 provider 的模型名）
	if defaultRoute, ok := cfg.Router["default"]; ok {
		parts := strings.SplitN(defaultRoute, ",", 2)
		if len(parts) == 2 {
			modelName := strings.TrimSpace(parts[1])
			if !seen[modelName] {
				seen[modelName] = true
				models = append(models, buildDefaultModelInfo(modelName))
			}
		}
	}

	return models
}

// buildDefaultModelInfo 为一个模型名生成合理的默认 ModelInfo
func buildDefaultModelInfo(slug string) ModelInfo {
	defaultContextWindow := int64(200000)
	defaultMaxContextWindow := int64(200000)
	defaultAutoCompactLimit := int64(160000)
	high := ReasoningEffortHigh
	supportsReasoningSummaries := true
	supportsParallelToolCalls := true

	return ModelInfo{
		Slug:                  slug,
		DisplayName:           slug,
		DefaultReasoningLevel: &high,
		SupportedReasoningLevels: []ReasoningEffortPreset{
			{Effort: ReasoningEffortNone, Description: "No extended thinking"},
			{Effort: ReasoningEffortLow, Description: "Low effort thinking"},
			{Effort: ReasoningEffortMedium, Description: "Medium effort thinking"},
			{Effort: ReasoningEffortHigh, Description: "High effort thinking"},
		},
		ShellType:                     ConfigShellToolTypeDefault,
		Visibility:                    ModelVisibilityList,
		SupportedInAPI:                true,
		Priority:                      0,
		BaseInstructions:              "",
		SupportsReasoningSummaries:    &supportsReasoningSummaries,
		DefaultReasoningSummary:       ReasoningSummaryConcise,
		SupportVerbosity:              false,
		WebSearchToolType:             WebSearchToolTypeText,
		TruncationPolicy:              TruncationPolicyConfig{Mode: TruncationModeTokens, Limit: 10000},
		SupportsParallelToolCalls:     &supportsParallelToolCalls,
		ContextWindow:                 &defaultContextWindow,
		MaxContextWindow:              &defaultMaxContextWindow,
		AutoCompactTokenLimit:         &defaultAutoCompactLimit,
		EffectiveContextWindowPercent: 95,
		ExperimentalSupportedTools:    []string{},
		InputModalities:               []InputModality{InputModalityText, InputModalityImage},
	}
}
