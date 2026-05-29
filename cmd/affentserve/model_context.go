package main

import (
	"context"
	"net/http"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/rs/zerolog"
)

const modelContextMetadataTimeout = 2 * time.Second

func resolveModelContextWindowFromProvider(cfg Config, logger zerolog.Logger) Config {
	if !cfg.ModelContextWindowAuto || cfg.ModelContextWindowTokens > 0 {
		return cfg
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelContextMetadataTimeout)
	defer cancel()

	llm := agent.NewLLMClient(cfg.BaseURL, cfg.APIKey, cfg.Model)
	llm.HTTP = &http.Client{Timeout: modelContextMetadataTimeout}
	meta, err := llm.FetchModelMetadata(ctx)
	if err != nil {
		logger.Debug().Err(err).Str("model", cfg.Model).Msg("model context window metadata unavailable")
		return cfg
	}
	if meta.ContextWindowTokens <= 0 {
		logger.Debug().Str("model", cfg.Model).Str("metadata_model", meta.ID).Msg("model context window metadata missing")
		return cfg
	}
	cfg.ModelContextWindowTokens = meta.ContextWindowTokens
	if cfg.CompactTriggerInputTokens == 0 && meta.AutoCompactTokenLimit > 0 {
		cfg.CompactTriggerInputTokens = agent.ClampAutoCompactTokenLimit(meta.AutoCompactTokenLimit, cfg.ModelContextWindowTokens, cfg.CompactTriggerInputPercent, reservedOutputTokensForConfig(cfg))
	}
	logger.Info().
		Str("model", cfg.Model).
		Str("metadata_model", meta.ID).
		Int("model_context_window_tokens", cfg.ModelContextWindowTokens).
		Int("effective_context_window_percent", meta.EffectiveContextWindowPercent).
		Int("auto_compact_token_limit", cfg.CompactTriggerInputTokens).
		Msg("model context window resolved from provider metadata")
	return cfg
}
