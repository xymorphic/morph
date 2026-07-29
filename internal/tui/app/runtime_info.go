package tui

import (
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/constants"
	"github.com/wandxy/morph/internal/profile"
	"github.com/wandxy/morph/pkg/str"
)

type runtimeInfo struct {
	Version           string
	Commit            string
	Profile           string
	Provider          string
	API               string
	Model             string
	SummaryProvider   string
	SummaryModel      string
	EmbeddingProvider string
	EmbeddingModel    string
	Storage           string
	Streaming         string
}

func defaultRuntimeInfo() runtimeInfo {
	return runtimeInfo{
		Version:   getRuntimeValue(constants.AppVersion, "dev"),
		Commit:    getRuntimeValue(constants.CommitHash, "unknown"),
		Profile:   profile.DefaultName,
		Storage:   constants.DefaultStorageBackend,
		Streaming: "on",
	}
}

func runtimeInfoFromConfig(cfg *config.Config) runtimeInfo {
	info := defaultRuntimeInfo()
	if cfg == nil {
		return info
	}

	cfg.Normalize()
	info.Provider = getRuntimeValue(cfg.Models.Main.Provider, info.Provider)
	info.API = getRuntimeValue(cfg.MainModelAPIEffective(), info.API)
	info.Model = getRuntimeValue(cfg.Models.Main.Name, info.Model)
	info.SummaryProvider = getRuntimeValue(cfg.SummaryProviderEffective(), info.SummaryProvider)
	info.SummaryModel = getRuntimeValue(cfg.SummaryModelEffective(), info.SummaryModel)
	info.EmbeddingProvider = getRuntimeValue(cfg.ModelEmbeddingProviderEffective(), info.EmbeddingProvider)
	info.EmbeddingModel = getRuntimeValue(cfg.Models.Embedding.Name, info.EmbeddingModel)
	info.Storage = getRuntimeValue(cfg.Storage.Backend, info.Storage)
	info.Streaming = getRuntimeBoolValue(cfg.StreamEnabled())
	nameValue := str.String(profile.Active().Name)
	if active := nameValue.Trim(); active != "" {
		info.Profile = active
	}

	return info
}

func getRuntimeValue(value string, fallback string) string {
	valueText := str.String(value).Trim()
	if valueText != "" {
		return valueText
	}
	fallbackValue := str.String(fallback)
	return fallbackValue.Trim()
}

func getRuntimeBoolValue(enabled bool) string {
	if enabled {
		return "on"
	}

	return "off"
}
