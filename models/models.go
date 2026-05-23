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
	SupportsReasoningSummaries    bool                    `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary       ReasoningSummary        `json:"default_reasoning_summary"`
	SupportVerbosity              bool                    `json:"support_verbosity"`
	DefaultVerbosity              *Verbosity              `json:"default_verbosity,omitempty"`
	ApplyPatchToolType            *ApplyPatchToolType     `json:"apply_patch_tool_type,omitempty"`
	WebSearchToolType             WebSearchToolType       `json:"web_search_tool_type"`
	TruncationPolicy              TruncationPolicyConfig  `json:"truncation_policy"`
	SupportsParallelToolCalls     bool                    `json:"supports_parallel_tool_calls"`
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
// 如果 provider 配置了 models_config，从 ~/.agr/<filename> 加载 JSON
// 否则根据 router 中配置的模型名生成默认的 ModelInfo
func LoadModels(cfg *config.Config) (*ModelsResponse, error) {
	var allModels []ModelInfo

	// 收集所有 provider 的 models_config 文件中的模型
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.ModelsConfig != "" {
			models, err := loadModelsFromFile(p.ModelsConfig)
			if err != nil {
				return nil, fmt.Errorf("加载 provider %s 的 models_config 失败: %w", p.Name, err)
			}
			allModels = append(allModels, models...)
		}
	}

	// 如果有任何 provider 配置了 models_config，直接返回文件中的模型
	if len(allModels) > 0 {
		return &ModelsResponse{Models: allModels}, nil
	}

	// 没有配置 models_config，根据 router 中的模型名生成默认 ModelInfo
	allModels = generateDefaultModels(cfg)
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
func applyDefaults(m *ModelInfo) {
	// truncation_policy: 如果 mode 为空，设置默认值
	if m.TruncationPolicy.Mode == "" {
		m.TruncationPolicy.Mode = TruncationModeTokens
	}
	if m.TruncationPolicy.Limit == 0 && m.ContextWindow != nil {
		m.TruncationPolicy.Limit = 10000
	}

	// input_modalities: 如果为空，默认支持 text
	if len(m.InputModalities) == 0 {
		m.InputModalities = []InputModality{InputModalityText}
	}

	// effective_context_window_percent: 如果为 0，默认 80
	if m.EffectiveContextWindowPercent == 0 {
		m.EffectiveContextWindowPercent = 95
	}

	// default_reasoning_summary: 如果为空，默认 concise
	if m.DefaultReasoningSummary == "" {
		m.DefaultReasoningSummary = ReasoningSummaryConcise
	}

	// experimental_supported_tools: 如果为 nil，设置为空切片确保 JSON 序列化为 []
	if m.ExperimentalSupportedTools == nil {
		m.ExperimentalSupportedTools = []string{}
	}

	// supported_reasoning_levels: 如果为 nil，设置为空切片
	if m.SupportedReasoningLevels == nil {
		m.SupportedReasoningLevels = []ReasoningEffortPreset{}
	}

	// web_search_tool_type: 如果为空，默认 text
	if m.WebSearchToolType == "" {
		m.WebSearchToolType = WebSearchToolTypeText
	}

	// shell_type: 如果为空，默认 default
	if m.ShellType == "" {
		m.ShellType = ConfigShellToolTypeDefault
	}

	// visibility: 如果为空，默认 list
	if m.Visibility == "" {
		m.Visibility = ModelVisibilityList
	}
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
		SupportsReasoningSummaries:    true,
		DefaultReasoningSummary:       ReasoningSummaryConcise,
		SupportVerbosity:              false,
		WebSearchToolType:             WebSearchToolTypeText,
		TruncationPolicy:              TruncationPolicyConfig{Mode: TruncationModeTokens, Limit: 10000},
		SupportsParallelToolCalls:     true,
		ContextWindow:                 &defaultContextWindow,
		MaxContextWindow:              &defaultMaxContextWindow,
		AutoCompactTokenLimit:         &defaultAutoCompactLimit,
		EffectiveContextWindowPercent: 95,
		ExperimentalSupportedTools:    []string{},
		InputModalities:               []InputModality{InputModalityText, InputModalityImage},
	}
}
